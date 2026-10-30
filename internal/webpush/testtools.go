//go:build testtools

package webpush

import (
	"errors"
	"net/http"
	"reflect"
)

// TestHTTPClient replaces only one test service's external request boundary.
// This interface and setter do not exist in ordinary production builds.
type TestHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

func (s *Service) SetHTTPClientForTests(client TestHTTPClient) error {
	if s == nil || s.transport == nil || client == nil {
		return errors.New("test push client is required")
	}
	value := reflect.ValueOf(client)
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func, reflect.Map, reflect.Slice:
		if value.IsNil() {
			return errors.New("test push client is required")
		}
	}
	s.transport.client = client
	return nil
}
