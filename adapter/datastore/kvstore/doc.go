// Package kvstore 把 [github.com/snight1983/ds-harness-go/storage] 的键值形态
// 接到 [github.com/snight1983/ds-harness-go/adapter/datastore] 的记录集上。
//
// 新增: 上游没有对应物。它是一层**只做翻译**的适配：一个 storage 单元就是一个
// datastore 记录集，一张表就是一张表，全局槽就是单例槽。本包里没有一句 SQL、
// 没有一个连接池、没有一句方言——那些全在 datastore 里，理由见那个包的文档。
//
// 依赖的方向：本包认识 storage 那道业务接口，storage **不认识本包**。
// 那棵树里没有、也不许有任何一处提到数据库。
//
// # 它翻的是什么
//
// 只有两件事：
//
//  1. **词汇。**datastore 用哨兵（[datastore.ErrVersionMismatch] 之类），
//     storage 用一套封闭的分类码（[storage.ErrorCode]）。这里对着翻一遍。
//     翻不出来的（连不上库、事务被中止）原样往上冒——它们在 storage 那套
//     封闭词汇里本来就没有位置。
//  2. **谁拥有介质。**[storage.Backend.Close] 的契约是「释放介质」，所以
//     [Backend.Close] 会把 [datastore.Medium] 连同它的连接池一起关掉。
//     一份介质因此归一个后端所有：要在同一个库里再开一份，给它另一个连接池。
//
// 除此之外一律直通。「同名单元不许重开」「表没声明过时删是空操作」「值必须是
// 合法 JSON」这些规则都在 datastore 那一层，本包不重写一遍——重写一遍就是
// 给分叉留一个位置。
package kvstore
