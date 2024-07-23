// File generated by hgctl. Modify as required.
// See: https://higress.io/zh-cn/docs/user/wasm-go#2-%E7%BC%96%E5%86%99-maingo-%E6%96%87%E4%BB%B6

package main

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/alibaba/higress/plugins/wasm-go/extensions/ai-proxy/config"
	"github.com/alibaba/higress/plugins/wasm-go/extensions/ai-proxy/provider"
	"github.com/alibaba/higress/plugins/wasm-go/extensions/ai-proxy/util"
	"github.com/alibaba/higress/plugins/wasm-go/pkg/wrapper"
	"github.com/higress-group/proxy-wasm-go-sdk/proxywasm"
	"github.com/higress-group/proxy-wasm-go-sdk/proxywasm/types"
	"github.com/tidwall/gjson"
)

const (
	pluginName = "ai-proxy"

	ctxKeyApiName = "apiKey"
)

func main() {
	wrapper.SetCtx(
		pluginName,
		wrapper.ParseConfigBy(parseConfig),
		wrapper.ProcessRequestHeadersBy(onHttpRequestHeader),
		wrapper.ProcessRequestBodyBy(onHttpRequestBody),
		wrapper.ProcessResponseHeadersBy(onHttpResponseHeaders),
		wrapper.ProcessStreamingResponseBodyBy(onStreamingResponseBody),
		wrapper.ProcessResponseBodyBy(onHttpResponseBody),
	)
}

func parseConfig(json gjson.Result, pluginConfig *config.PluginConfig, log wrapper.Log) error {
	// log.Debugf("loading config: %s", json.String())

	pluginConfig.FromJson(json)
	if err := pluginConfig.Validate(); err != nil {
		return err
	}
	if err := pluginConfig.Complete(); err != nil {
		return err
	}
	return nil
}

func onHttpRequestHeader(ctx wrapper.HttpContext, pluginConfig config.PluginConfig, log wrapper.Log) types.Action {
	activeProvider := pluginConfig.GetProvider()

	if activeProvider == nil {
		log.Debugf("[onHttpRequestHeader] no active provider, skip processing")
		ctx.DontReadRequestBody()
		return types.ActionContinue
	}

	log.Debugf("[onHttpRequestHeader] provider=%s", activeProvider.GetProviderType())

	rawPath := ctx.Path()
	path, _ := url.Parse(rawPath)
	apiName := getOpenAiApiName(path.Path)
	if apiName == "" {
		log.Debugf("[onHttpRequestHeader] unsupported path: %s", path.Path)
		_ = util.SendResponse(404, "ai-proxy.unknown_api", util.MimeTypeTextPlain, "API not found: "+path.Path)
		return types.ActionContinue
	}
	ctx.SetContext(ctxKeyApiName, apiName)

	if handler, ok := activeProvider.(provider.RequestHeadersHandler); ok {
		// Disable the route re-calculation since the plugin may modify some headers related to  the chosen route.
		ctx.DisableReroute()

		action, err := handler.OnRequestHeaders(ctx, apiName, log)
		if err == nil {
			return action
		}
		_ = util.SendResponse(500, "ai-proxy.proc_req_headers_failed", util.MimeTypeTextPlain, fmt.Sprintf("failed to process request headers: %v", err))
		return types.ActionContinue
	}

	if _, needHandleBody := activeProvider.(provider.RequestBodyHandler); needHandleBody {
		ctx.DontReadRequestBody()
	}

	return types.ActionContinue
}

func onHttpRequestBody(ctx wrapper.HttpContext, pluginConfig config.PluginConfig, body []byte, log wrapper.Log) types.Action {
	activeProvider := pluginConfig.GetProvider()

	if activeProvider == nil {
		log.Debugf("[onHttpRequestBody] no active provider, skip processing")
		return types.ActionContinue
	}

	log.Debugf("[onHttpRequestBody] provider=%s", activeProvider.GetProviderType())

	if handler, ok := activeProvider.(provider.RequestBodyHandler); ok {
		apiName, _ := ctx.GetContext(ctxKeyApiName).(provider.ApiName)
		action, err := handler.OnRequestBody(ctx, apiName, body, log)
		if err == nil {
			return action
		}
		_ = util.SendResponse(500, "ai-proxy.proc_req_body_failed", util.MimeTypeTextPlain, fmt.Sprintf("failed to process request body: %v", err))
		return types.ActionContinue
	}
	return types.ActionContinue
}

func onHttpResponseHeaders(ctx wrapper.HttpContext, pluginConfig config.PluginConfig, log wrapper.Log) types.Action {
	activeProvider := pluginConfig.GetProvider()

	if activeProvider == nil {
		log.Debugf("[onHttpResponseHeaders] no active provider, skip processing")
		ctx.DontReadResponseBody()
		return types.ActionContinue
	}

	log.Debugf("[onHttpResponseHeaders] provider=%s", activeProvider.GetProviderType())

	status, err := proxywasm.GetHttpResponseHeader(":status")
	if err != nil || status != "200" {
		if err != nil {
			log.Errorf("unable to load :status header from response: %v", err)
		}
		ctx.DontReadResponseBody()
		return types.ActionContinue
	}

	if handler, ok := activeProvider.(provider.ResponseHeadersHandler); ok {
		apiName, _ := ctx.GetContext(ctxKeyApiName).(provider.ApiName)
		action, err := handler.OnResponseHeaders(ctx, apiName, log)
		if err == nil {
			checkStream(&ctx, &log)
			return action
		}
		_ = util.SendResponse(500, "ai-proxy.proc_resp_headers_failed", util.MimeTypeTextPlain, fmt.Sprintf("failed to process response headers: %v", err))
		return types.ActionContinue
	}

	checkStream(&ctx, &log)
	_, needHandleBody := activeProvider.(provider.ResponseBodyHandler)
	_, needHandleStreamingBody := activeProvider.(provider.StreamingResponseBodyHandler)
	if !needHandleBody && !needHandleStreamingBody {
		ctx.DontReadResponseBody()
	} else if !needHandleStreamingBody {
		ctx.BufferResponseBody()
	}

	return types.ActionContinue
}

func onStreamingResponseBody(ctx wrapper.HttpContext, pluginConfig config.PluginConfig, chunk []byte, isLastChunk bool, log wrapper.Log) []byte {
	activeProvider := pluginConfig.GetProvider()

	if activeProvider == nil {
		log.Debugf("[onStreamingResponseBody] no active provider, skip processing")
		return chunk
	}

	log.Debugf("[onStreamingResponseBody] provider=%s", activeProvider.GetProviderType())
	log.Debugf("isLastChunk=%v chunk: %s", isLastChunk, string(chunk))

	if handler, ok := activeProvider.(provider.StreamingResponseBodyHandler); ok {
		apiName, _ := ctx.GetContext(ctxKeyApiName).(provider.ApiName)
		modifiedChunk, err := handler.OnStreamingResponseBody(ctx, apiName, chunk, isLastChunk, log)
		if err == nil && modifiedChunk != nil {
			return modifiedChunk
		}
		return chunk
	}
	return chunk
}

func onHttpResponseBody(ctx wrapper.HttpContext, pluginConfig config.PluginConfig, body []byte, log wrapper.Log) types.Action {
	activeProvider := pluginConfig.GetProvider()

	if activeProvider == nil {
		log.Debugf("[onHttpResponseBody] no active provider, skip processing")
		return types.ActionContinue
	}

	log.Debugf("[onHttpResponseBody] provider=%s", activeProvider.GetProviderType())
	//log.Debugf("response body: %s", string(body))

	if handler, ok := activeProvider.(provider.ResponseBodyHandler); ok {
		apiName, _ := ctx.GetContext(ctxKeyApiName).(provider.ApiName)
		action, err := handler.OnResponseBody(ctx, apiName, body, log)
		if err == nil {
			return action
		}
		_ = util.SendResponse(500, "ai-proxy.proc_resp_body_failed", util.MimeTypeTextPlain, fmt.Sprintf("failed to process response body: %v", err))
		return types.ActionContinue
	}
	return types.ActionContinue
}

func getOpenAiApiName(path string) provider.ApiName {
	if strings.HasSuffix(path, "/v1/chat/completions") {
		return provider.ApiNameChatCompletion
	}
	if strings.HasSuffix(path, "/v1/embeddings") {
		return provider.ApiNameEmbeddings
	}
	return ""
}

func checkStream(ctx *wrapper.HttpContext, log *wrapper.Log) {
	contentType, err := proxywasm.GetHttpResponseHeader("Content-Type")
	if err != nil || !strings.HasPrefix(contentType, "text/event-stream") {
		if err != nil {
			log.Errorf("unable to load content-type header from response: %v", err)
		}
		(*ctx).BufferResponseBody()
	}
}
