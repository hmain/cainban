package main

import (
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/hmain/cainban/src/systems/store"
)

// ensureDynamoBackend forces CAINBAN_BACKEND=dynamodb for this process. Returns
// the previous value (for completeness / testability).
func ensureDynamoBackend() string {
	prev := os.Getenv(store.EnvBackend)
	_ = os.Setenv(store.EnvBackend, store.BackendDynamoDB)
	return prev
}

// functionURLToAPIGatewayV2 maps a Lambda Function URL request (payload format
// 2.0) onto the API Gateway HTTP API v2 request type the httpadapter consumes.
// The two payloads share format 2.0, so the mapping is field-for-field.
func functionURLToAPIGatewayV2(req events.LambdaFunctionURLRequest) events.APIGatewayV2HTTPRequest {
	return events.APIGatewayV2HTTPRequest{
		Version:               req.Version,
		RawPath:               req.RawPath,
		RawQueryString:        req.RawQueryString,
		Cookies:               req.Cookies,
		Headers:               req.Headers,
		QueryStringParameters: req.QueryStringParameters,
		Body:                  req.Body,
		IsBase64Encoded:       req.IsBase64Encoded,
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{
				Method:    req.RequestContext.HTTP.Method,
				Path:      req.RequestContext.HTTP.Path,
				Protocol:  req.RequestContext.HTTP.Protocol,
				SourceIP:  req.RequestContext.HTTP.SourceIP,
				UserAgent: req.RequestContext.HTTP.UserAgent,
			},
		},
	}
}

// apiGatewayV2ToFunctionURL maps the adapter's API Gateway v2 response back onto
// the Function URL response type.
func apiGatewayV2ToFunctionURL(resp events.APIGatewayV2HTTPResponse) events.LambdaFunctionURLResponse {
	return events.LambdaFunctionURLResponse{
		StatusCode:      resp.StatusCode,
		Headers:         resp.Headers,
		Body:            resp.Body,
		IsBase64Encoded: resp.IsBase64Encoded,
		Cookies:         resp.Cookies,
	}
}
