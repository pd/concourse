// Code generated by counterfeiter. DO NOT EDIT.
package authfakes

import (
	"net/http"
	"sync"
)

type FakeHandler struct {
	ServeHTTPStub        func(http.ResponseWriter, *http.Request)
	serveHTTPMutex       sync.RWMutex
	serveHTTPArgsForCall []struct {
		arg1 http.ResponseWriter
		arg2 *http.Request
	}
	invocations      map[string][][]interface{}
	invocationsMutex sync.RWMutex
}

func (fake *FakeHandler) ServeHTTP(arg1 http.ResponseWriter, arg2 *http.Request) {
	fake.serveHTTPMutex.Lock()
	fake.serveHTTPArgsForCall = append(fake.serveHTTPArgsForCall, struct {
		arg1 http.ResponseWriter
		arg2 *http.Request
	}{arg1, arg2})
	stub := fake.ServeHTTPStub
	fake.recordInvocation("ServeHTTP", []interface{}{arg1, arg2})
	fake.serveHTTPMutex.Unlock()
	if stub != nil {
		fake.ServeHTTPStub(arg1, arg2)
	}
}

func (fake *FakeHandler) ServeHTTPCallCount() int {
	fake.serveHTTPMutex.RLock()
	defer fake.serveHTTPMutex.RUnlock()
	return len(fake.serveHTTPArgsForCall)
}

func (fake *FakeHandler) ServeHTTPCalls(stub func(http.ResponseWriter, *http.Request)) {
	fake.serveHTTPMutex.Lock()
	defer fake.serveHTTPMutex.Unlock()
	fake.ServeHTTPStub = stub
}

func (fake *FakeHandler) ServeHTTPArgsForCall(i int) (http.ResponseWriter, *http.Request) {
	fake.serveHTTPMutex.RLock()
	defer fake.serveHTTPMutex.RUnlock()
	argsForCall := fake.serveHTTPArgsForCall[i]
	return argsForCall.arg1, argsForCall.arg2
}

func (fake *FakeHandler) Invocations() map[string][][]interface{} {
	fake.invocationsMutex.RLock()
	defer fake.invocationsMutex.RUnlock()
	fake.serveHTTPMutex.RLock()
	defer fake.serveHTTPMutex.RUnlock()
	copiedInvocations := map[string][][]interface{}{}
	for key, value := range fake.invocations {
		copiedInvocations[key] = value
	}
	return copiedInvocations
}

func (fake *FakeHandler) recordInvocation(key string, args []interface{}) {
	fake.invocationsMutex.Lock()
	defer fake.invocationsMutex.Unlock()
	if fake.invocations == nil {
		fake.invocations = map[string][][]interface{}{}
	}
	if fake.invocations[key] == nil {
		fake.invocations[key] = [][]interface{}{}
	}
	fake.invocations[key] = append(fake.invocations[key], args)
}

var _ http.Handler = new(FakeHandler)
