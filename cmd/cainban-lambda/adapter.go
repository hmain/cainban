package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/store"
)

// Auth env vars the CDK stack sets from the Cognito user pool. The JWKS URL is
// derived from the issuer when not set explicitly (Cognito's standard path).
const (
	envAuthIssuer   = "CAINBAN_AUTH_ISSUER"   // e.g. https://cognito-idp.<region>.amazonaws.com/<poolId>
	envAuthAudience = "CAINBAN_AUTH_AUDIENCE" // Cognito app client id
	envAuthJWKSURL  = "CAINBAN_AUTH_JWKS_URL" // optional override; else <issuer>/.well-known/jwks.json
)

// buildResolver constructs the signature-first auth resolver from env. It fails
// (rather than serving an unauthenticated endpoint) if issuer/audience are
// missing — there is no fail-open path.
func buildResolver() (*auth.Resolver, error) {
	issuer := strings.TrimSpace(os.Getenv(envAuthIssuer))
	audience := strings.TrimSpace(os.Getenv(envAuthAudience))
	if issuer == "" || audience == "" {
		return nil, fmt.Errorf("both %s and %s must be set (no unauthenticated endpoint)", envAuthIssuer, envAuthAudience)
	}
	jwksURL := strings.TrimSpace(os.Getenv(envAuthJWKSURL))
	if jwksURL == "" {
		jwksURL = strings.TrimRight(issuer, "/") + "/.well-known/jwks.json"
	}

	validator, err := auth.NewValidator(auth.Config{
		Issuer:   issuer,
		Audience: audience,
		Keys:     auth.NewJWKSCache(jwksURL),
	})
	if err != nil {
		return nil, err
	}
	return auth.NewResolver(validator), nil
}

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
