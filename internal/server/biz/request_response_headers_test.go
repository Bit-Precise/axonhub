package biz

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateResponseHeadersMasksSensitiveValues(t *testing.T) {
	ctx, client, svc, _, execution := externalFinalizationFixture(t)

	req, err := client.Request.Get(ctx, execution.RequestID)
	require.NoError(t, err)
	headers := http.Header{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer secret"},
		"X-Request-ID":  {"request-id"},
	}

	require.NoError(t, svc.UpdateRequestResponseHeaders(ctx, req.ID, headers))
	require.NoError(t, svc.UpdateRequestExecutionResponseHeaders(ctx, execution.ID, headers))

	req = client.Request.GetX(ctx, req.ID)
	require.JSONEq(t, `{"Content-Type":["application/json"],"Authorization":["******"],"X-Request-ID":["request-id"]}`, string(req.ResponseHeaders))

	execution = client.RequestExecution.GetX(ctx, execution.ID)
	require.JSONEq(t, `{"Content-Type":["application/json"],"Authorization":["******"],"X-Request-ID":["request-id"]}`, string(execution.ResponseHeaders))
}
