package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Product struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Price int    `json:"price"`
}

// queryProduct is ordinary business code. It knows nothing about MCP or models.
func queryProduct(productID string) (Product, error) {
	if productID != "SKU-100" {
		return Product{}, errors.New("product not found")
	}
	return Product{ID: "SKU-100", Name: "Mechanical Keyboard", Price: 59900}, nil
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "product-server",
		Version: "1.0.0",
	}, nil)

	// This wrapper turns the ordinary function into an MCP tool.
	server.AddTool(&mcp.Tool{
		Name:        "query_product",
		Description: "Query a product by productId. Price is returned in cents.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"productId": {"type": "string"}
			},
			"required": ["productId"],
			"additionalProperties": false
		}`),
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var input struct {
			ProductID string `json:"productId"`
		}
		if err := json.Unmarshal(request.Params.Arguments, &input); err != nil {
			return nil, err
		}

		product, err := queryProduct(input.ProductID)
		if err != nil {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
			}, nil
		}

		output, err := json.Marshal(product)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{
			Content:           []mcp.Content{&mcp.TextContent{Text: string(output)}},
			StructuredContent: product,
		}, nil
	})

	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		nil,
	)

	log.Println("MCP server: http://localhost:8080/mcp")
	log.Fatal(http.ListenAndServe(":8080", handler))
}
