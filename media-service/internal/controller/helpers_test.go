package controller

import (
	"encoding/json"
	"net/http/httptest"
)

func decode(recorder *httptest.ResponseRecorder, into any) error {
	return json.Unmarshal(recorder.Body.Bytes(), into)
}
