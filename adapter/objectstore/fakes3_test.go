// 本文件是一台**跑在进程里**的 S3 服务端，只给测试用。
//
// 为什么要自己写一台：这个包是 I/O 包，它的全部行为都长在「我们发出去什么头、
// 服务端怎么回」上面。用一个假的 minio 客户端接口去测，测到的是我们自己的桩，
// 不是那些头——而 `If-None-Match: *` 到底有没有发出去、412 有没有被认出来，
// 恰恰是这个包最要紧的两件事。
//
// 它实现的是真实 minio-go 会发的那几个请求：HEAD / GET / PUT / DELETE 对象，
// 加上 ListObjectsV2。**不做鉴权**——签名对不对不是这个包的行为。
//
// 它也让「服务端不认条件写」这件事可以被测到：把 [fakeS3.IgnoreConditionals]
// 打开，它就变成一台 2024 年之前的 MinIO，于是
// [Store.VerifyCreateIfAbsent] 那条自测有真东西可抓。

package objectstore

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeObject 是桶里的一个对象。
type fakeObject struct {
	content []byte
	etag    string
	modTime time.Time
}

// fakeS3 是那台进程内的服务端。
type fakeS3 struct {
	server *httptest.Server

	mutex   sync.Mutex
	bucket  string
	objects map[string]fakeObject

	// IgnoreConditionals 为真时，服务端把 If-Match / If-None-Match 当不存在。
	// 那正是早于 RELEASE.2024-09-13 的 MinIO 的行为，也是
	// [Store.VerifyCreateIfAbsent] 要抓的那件事。
	IgnoreConditionals bool

	// onPut 非 nil 时在每次 PUT 落库**之前**调一次，用来在读和写之间插一手。
	// 乐观并发那几条用例全靠它——没有它就没办法制造出「读完之后、写之前
	// 有人改了这个对象」这个时刻。
	onPut func(key string)

	// cutBodyAfter 大于零时，GET 只写这么多字节就撒手，但 Content-Length 照实报。
	//
	// 这是「传到一半服务端没了」的最小复现：Go 的 http 服务端发现写出的字节
	// 少于声明的长度，会把连接断掉，客户端那侧于是在读到一半时拿到
	// [io.ErrUnexpectedEOF]。没有它就验不到流式读取里那条「不是 EOF 的读错误」——
	// 而那正是一次真实的网络中断在这个包里长的样子。
	cutBodyAfter int

	// intercept 非 nil 时在每个请求最前面调一次；返回 true 表示这次请求由它答复。
	//
	// 有它才验得到错误翻译层在**真实调用路径上**的那几支：一次 403 从 minio-go
	// 一路传回来长什么样，和构造一个 ErrorResponse 喂进 translate 不是同一件事。
	// 而 403 这种事没办法靠正常的请求造出来——这台服务端不做鉴权。
	intercept func(method string, key string) (status int, code string, handled bool)
}

// newFakeS3 起一台服务端，并在用例结束时关掉。
func newFakeS3(t *testing.T) *fakeS3 {
	t.Helper()

	fake := &fakeS3{bucket: "world", objects: make(map[string]fakeObject)}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	return fake
}

// store 建一个连到这台服务端的 [Store]。
func (f *fakeS3) store(t *testing.T, prefix string) *Store {
	t.Helper()
	return f.storeWith(t, Config{Prefix: prefix})
}

// storeWith 同 store，但让用例改上限和块大小。
//
// Endpoint / Bucket / 凭据 / 区域由这里填，用例只该关心它要调的那两个旋钮。
func (f *fakeS3) storeWith(t *testing.T, config Config) *Store {
	t.Helper()

	config.Endpoint = strings.TrimPrefix(f.server.URL, "http://")
	config.Bucket = f.bucket
	config.AccessKey = "key"
	config.SecretKey = "secret"
	// 区域写死，省掉 minio-go 启动时那次 GetBucketLocation 往返。
	config.Region = "us-east-1"

	store, err := New(config)
	if err != nil {
		t.Fatalf("建后端不该失败：%v", err)
	}
	return store
}

// seed 直接往桶里放一个对象，绕过被测代码。
func (f *fakeS3) seed(key string, content string) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.put(key, []byte(content))
}

// seedBytes 同 seed，但放的是原始字节（用来造非 UTF-8 的内容）。
func (f *fakeS3) seedBytes(key string, content []byte) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.put(key, content)
}

// etagOfKey 给出一个对象当前的 ETag，没有这个对象时给空串。
func (f *fakeS3) etagOfKey(key string) string {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	return f.objects[key].etag
}

// keys 给出桶里所有的键，已排序。
func (f *fakeS3) keys() []string {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	all := make([]string, 0, len(f.objects))
	for key := range f.objects {
		all = append(all, key)
	}
	sort.Strings(all)
	return all
}

// put 落一个对象。调用方必须已经持锁。
func (f *fakeS3) put(key string, content []byte) fakeObject {
	sum := md5.Sum(content)
	object := fakeObject{
		content: content,
		etag:    hex.EncodeToString(sum[:]),
		modTime: time.Now().UTC(),
	}
	f.objects[key] = object
	return object
}

func (f *fakeS3) serve(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/")
	bucket, key, _ := strings.Cut(path, "/")

	if f.intercept != nil {
		if status, code, handled := f.intercept(request.Method, key); handled {
			writeS3Error(writer, status, code, "由用例注入的失败")
			return
		}
	}

	if bucket != f.bucket {
		writeS3Error(writer, http.StatusNotFound, "NoSuchBucket", "没有这个桶")
		return
	}

	// 键为空的请求是桶级操作，这里只会碰到 ListObjectsV2。
	if key == "" {
		f.list(writer, request)
		return
	}

	switch request.Method {
	case http.MethodHead, http.MethodGet:
		f.get(writer, request, key)
	case http.MethodPut:
		f.putObject(writer, request, key)
	case http.MethodDelete:
		f.deleteObject(writer, key)
	default:
		writeS3Error(writer, http.StatusMethodNotAllowed, "MethodNotAllowed", "不支持的方法")
	}
}

func (f *fakeS3) get(writer http.ResponseWriter, request *http.Request, key string) {
	f.mutex.Lock()
	object, found := f.objects[key]
	f.mutex.Unlock()

	if !found {
		writeS3Error(writer, http.StatusNotFound, "NoSuchKey", "没有这个键")
		return
	}

	writer.Header().Set("ETag", `"`+object.etag+`"`)
	writer.Header().Set("Content-Type", "text/plain")
	writer.Header().Set("Last-Modified", object.modTime.Format(http.TimeFormat))
	// Content-Length 必须报：minio-go 在解不出它时直接报 InternalError，
	// 不会退回到分块读。这也正是 [Store.fetch] 第二道上限判定走不到的原因，
	// 见那里的注释。
	writer.Header().Set("Content-Length", strconv.Itoa(len(object.content)))
	writer.WriteHeader(http.StatusOK)

	if request.Method == http.MethodGet {
		content := object.content
		if f.cutBodyAfter > 0 && f.cutBodyAfter < len(content) {
			content = content[:f.cutBodyAfter]
		}
		_, _ = writer.Write(content)
	}
}

// readRequestBody 读出 PUT 的请求体，必要时先解掉 aws-chunked 那层外壳。
//
// 走 http 而不是 https 时，minio-go 用的是流式签名：请求体被切成
// `<十六进制长度>;chunk-signature=<签名>\r\n<数据>\r\n` 这样一串，最后一块长度为零。
// 真的 S3 服务端会解掉这层，这里也得解——不解的话存进去的是签名文本，
// 而每一条写入用例都会在「内容对不对」上失败，看上去像是这个包写错了。
//
// **签名本身不验**：这台服务端不做鉴权，签名对不对不是这个包的行为。
func readRequestBody(request *http.Request) ([]byte, error) {
	if !strings.HasPrefix(request.Header.Get("x-amz-content-sha256"), "STREAMING-") {
		return io.ReadAll(request.Body)
	}

	reader := bufio.NewReader(request.Body)
	var content []byte
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil, err
		}
		size, err := strconv.ParseInt(strings.SplitN(strings.TrimRight(line, "\r\n"), ";", 2)[0], 16, 64)
		if err != nil {
			return nil, err
		}
		if size == 0 {
			return content, nil
		}

		chunk := make([]byte, size)
		if _, err := io.ReadFull(reader, chunk); err != nil {
			return nil, err
		}
		content = append(content, chunk...)
		if _, err := reader.Discard(2); err != nil { // 块尾的 CRLF
			return nil, err
		}
	}
}

func (f *fakeS3) putObject(writer http.ResponseWriter, request *http.Request, key string) {
	content, err := readRequestBody(request)
	if err != nil {
		writeS3Error(writer, http.StatusBadRequest, "IncompleteBody", "读请求体失败")
		return
	}

	if f.onPut != nil {
		f.onPut(key)
	}

	f.mutex.Lock()
	defer f.mutex.Unlock()

	existing, exists := f.objects[key]
	if !f.IgnoreConditionals {
		// If-None-Match: * ——只允许创建。
		if request.Header.Get("If-None-Match") == "*" && exists {
			writeS3Error(writer, http.StatusPreconditionFailed, "PreconditionFailed", "对象已经存在")
			return
		}
		// If-Match: "etag" ——只允许落在这一份上。
		if want := strings.Trim(request.Header.Get("If-Match"), `"`); want != "" && want != "*" {
			if !exists || existing.etag != want {
				writeS3Error(writer, http.StatusPreconditionFailed, "PreconditionFailed", "版本对不上")
				return
			}
		}
	}

	object := f.put(key, content)
	writer.Header().Set("ETag", `"`+object.etag+`"`)
	writer.WriteHeader(http.StatusOK)
}

func (f *fakeS3) deleteObject(writer http.ResponseWriter, key string) {
	f.mutex.Lock()
	delete(f.objects, key)
	f.mutex.Unlock()
	writer.WriteHeader(http.StatusNoContent)
}

// listResult 是 ListObjectsV2 的响应体。字段顺序和标签照 S3 的 XML 来，
// 因为 minio-go 是照那份 schema 解的。
type listResult struct {
	XMLName        xml.Name `xml:"ListBucketResult"`
	Name           string
	Prefix         string
	KeyCount       int
	MaxKeys        int
	Delimiter      string
	IsTruncated    bool
	Contents       []listContents
	CommonPrefixes []listPrefix
}

type listContents struct {
	XMLName      xml.Name `xml:"Contents"`
	Key          string
	LastModified string
	ETag         string
	Size         int64
	StorageClass string
}

type listPrefix struct {
	XMLName xml.Name `xml:"CommonPrefixes"`
	Prefix  string
}

func (f *fakeS3) list(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	prefix := query.Get("prefix")
	delimiter := query.Get("delimiter")
	maxKeys := 1000
	if raw := query.Get("max-keys"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			maxKeys = parsed
		}
	}

	f.mutex.Lock()
	keys := make([]string, 0, len(f.objects))
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	result := listResult{Name: f.bucket, Prefix: prefix, MaxKeys: maxKeys, Delimiter: delimiter}
	seenPrefix := make(map[string]bool)
	for _, key := range keys {
		if result.KeyCount >= maxKeys {
			// 截断了也照实说 false：这几条用例都不翻页，报 true 只会让
			// minio-go 拿着一个我们不打算兑现的 continuation-token 再来一次。
			break
		}
		rest := strings.TrimPrefix(key, prefix)
		if delimiter != "" {
			if at := strings.Index(rest, delimiter); at >= 0 {
				common := prefix + rest[:at+len(delimiter)]
				if !seenPrefix[common] {
					seenPrefix[common] = true
					result.CommonPrefixes = append(result.CommonPrefixes, listPrefix{Prefix: common})
					result.KeyCount++
				}
				continue
			}
		}
		object := f.objects[key]
		result.Contents = append(result.Contents, listContents{
			Key:          key,
			LastModified: object.modTime.Format(time.RFC3339),
			ETag:         `"` + object.etag + `"`,
			Size:         int64(len(object.content)),
			StorageClass: "STANDARD",
		})
		result.KeyCount++
	}
	f.mutex.Unlock()

	body, err := xml.Marshal(result)
	if err != nil {
		writeS3Error(writer, http.StatusInternalServerError, "InternalError", err.Error())
		return
	}
	writer.Header().Set("Content-Type", "application/xml")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(body)
}

// s3Error 是 S3 的错误响应体。minio-go 就是照这个形状解出 ErrorResponse.Code 的，
// 而这个包的错误翻译层全靠那个 Code。
type s3Error struct {
	XMLName xml.Name `xml:"Error"`
	Code    string
	Message string
}

func writeS3Error(writer http.ResponseWriter, status int, code string, message string) {
	body, _ := xml.Marshal(s3Error{Code: code, Message: message})
	writer.Header().Set("Content-Type", "application/xml")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}
