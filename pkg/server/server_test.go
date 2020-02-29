// Copyright 2018 Envoyproxy Authors
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.

package server_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"

	envoy_api_v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	envoy_config_core_v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoy_service_discovery_v3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/apiversions"
	"github.com/envoyproxy/go-control-plane/pkg/cache"
	"github.com/envoyproxy/go-control-plane/pkg/server"
	"github.com/envoyproxy/go-control-plane/pkg/test/resource"
)

type mockConfigWatcher struct {
	counts     map[string]int
	responses  map[string][]cache.Response
	closeWatch bool
}

func (config *mockConfigWatcher) CreateWatch(req apiversions.DiscoveryRequest) (chan cache.Response, func()) {
	config.counts[req.GetTypeUrl()] = config.counts[req.GetTypeUrl()] + 1
	out := make(chan cache.Response, 1)
	if len(config.responses[req.GetTypeUrl()]) > 0 {
		out <- config.responses[req.GetTypeUrl()][0]
		config.responses[req.GetTypeUrl()] = config.responses[req.GetTypeUrl()][1:]
	} else if config.closeWatch {
		close(out)
	}
	return out, func() {}
}

func (config *mockConfigWatcher) Fetch(ctx context.Context, req apiversions.DiscoveryRequest) (*cache.Response, error) {
	if len(config.responses[req.GetTypeUrl()]) > 0 {
		out := config.responses[req.GetTypeUrl()][0]
		config.responses[req.GetTypeUrl()] = config.responses[req.GetTypeUrl()][1:]
		return &out, nil
	}
	return nil, errors.New("missing")
}

func (config *mockConfigWatcher) GetStatusInfo(string) cache.StatusInfo { return nil }
func (config *mockConfigWatcher) GetStatusKeys() []string               { return nil }

func makeMockConfigWatcher() *mockConfigWatcher {
	return &mockConfigWatcher{
		counts: make(map[string]int),
	}
}

type callbacks struct {
	fetchReq      int
	fetchResp     int
	callbackError bool
}

func (c *callbacks) OnStreamOpen(context.Context, int64, string) error {
	if c.callbackError {
		return errors.New("stream open error")
	}
	return nil
}
func (c *callbacks) OnStreamClosed(int64)                                      {}
func (c *callbacks) OnStreamRequest(int64, apiversions.DiscoveryRequest) error { return nil }
func (c *callbacks) OnStreamResponse(int64, apiversions.DiscoveryRequest, apiversions.DiscoveryResponse) {
}
func (c *callbacks) OnFetchRequest(context.Context, apiversions.DiscoveryRequest) error {
	if c.callbackError {
		return errors.New("fetch request error")
	}
	c.fetchReq++
	return nil
}
func (c *callbacks) OnFetchResponse(apiversions.DiscoveryRequest, apiversions.DiscoveryResponse) {
	c.fetchResp++
}

type mockStream struct {
	s2 *mockStreamV2
	s3 *mockStreamV3
}

type mockStreamV2 struct {
	t         *testing.T
	ctx       context.Context
	recv      chan *envoy_api_v2.DiscoveryRequest
	sent      chan *envoy_api_v2.DiscoveryResponse
	nonce     int
	sendError bool
	grpc.ServerStream
}

func (stream *mockStreamV2) Context() context.Context {
	return stream.ctx
}

func (stream *mockStreamV2) Send(resp *envoy_api_v2.DiscoveryResponse) error {
	// check that nonce is monotonically incrementing
	stream.nonce = stream.nonce + 1
	if resp.GetNonce() != fmt.Sprintf("%d", stream.nonce) {
		stream.t.Errorf("Nonce => got %q, want %d", resp.GetNonce(), stream.nonce)
	}
	// check that version is set
	if resp.GetVersionInfo() == "" {
		stream.t.Error("VersionInfo => got none, want non-empty")
	}
	// check resources are non-empty
	if len(resp.Resources) == 0 {
		stream.t.Error("Resources => got none, want non-empty")
	}
	// check that type URL matches in resources
	if resp.GetTypeUrl() == "" {
		stream.t.Error("TypeUrl => got none, want non-empty")
	}
	for _, res := range resp.Resources {
		if res.TypeUrl != resp.TypeUrl {
			stream.t.Errorf("TypeUrl => got %q, want %q", res.TypeUrl, resp.TypeUrl)
		}
	}
	stream.sent <- resp
	if stream.sendError {
		return errors.New("send error")
	}
	return nil
}

func (stream *mockStreamV2) Recv() (*envoy_api_v2.DiscoveryRequest, error) {
	req, more := <-stream.recv
	if !more {
		return nil, errors.New("empty")
	}
	return req, nil
}

type mockStreamV3 struct {
	t         *testing.T
	ctx       context.Context
	recv      chan *envoy_service_discovery_v3.DiscoveryRequest
	sent      chan *envoy_service_discovery_v3.DiscoveryResponse
	nonce     int
	sendError bool
	grpc.ServerStream
}

func (stream *mockStreamV3) Recv() (*envoy_service_discovery_v3.DiscoveryRequest, error) {
	req, more := <-stream.recv
	if !more {
		return nil, errors.New("empty")
	}
	return req, nil
}

func (stream *mockStreamV3) Context() context.Context {
	return stream.ctx
}

func (stream *mockStreamV3) Send(resp *envoy_service_discovery_v3.DiscoveryResponse) error {
	// check that nonce is monotonically incrementing
	stream.nonce = stream.nonce + 1
	if resp.GetNonce() != fmt.Sprintf("%d", stream.nonce) {
		stream.t.Errorf("Nonce => got %q, want %d", resp.GetNonce(), stream.nonce)
	}
	// check that version is set
	if resp.GetVersionInfo() == "" {
		stream.t.Error("VersionInfo => got none, want non-empty")
	}
	// check resources are non-empty
	if len(resp.Resources) == 0 {
		stream.t.Error("Resources => got none, want non-empty")
	}
	// check that type URL matches in resources
	if resp.GetTypeUrl() == "" {
		stream.t.Error("TypeUrl => got none, want non-empty")
	}
	for _, res := range resp.Resources {
		if res.TypeUrl != resp.TypeUrl {
			stream.t.Errorf("TypeUrl => got %q, want %q", res.TypeUrl, resp.TypeUrl)
		}
	}
	stream.sent <- resp
	if stream.sendError {
		return errors.New("send error")
	}
	return nil
}

func makeMockStream(t *testing.T) *mockStream {
	return &mockStream{
		s2: &mockStreamV2{
			t:    t,
			ctx:  context.Background(),
			sent: make(chan *envoy_api_v2.DiscoveryResponse, 10),
			recv: make(chan *envoy_api_v2.DiscoveryRequest, 10),
		},
		s3: &mockStreamV3{
			t:    t,
			ctx:  context.Background(),
			sent: make(chan *envoy_service_discovery_v3.DiscoveryResponse, 10),
			recv: make(chan *envoy_service_discovery_v3.DiscoveryRequest, 10),
		},
	}
}

const (
	clusterName  = "cluster0"
	routeName    = "route0"
	listenerName = "listener0"
)

var (
	nodeV2 = &core.Node{
		Id:      "test-id",
		Cluster: "test-cluster",
	}
	nodeV3 = &envoy_config_core_v3.Node{
		Id:      "test-id",
		Cluster: "test-cluster",
	}
	endpoint   = resource.MakeEndpoint(clusterName, 8080)
	cluster    = resource.MakeCluster(resource.Ads, clusterName)
	route      = resource.MakeRoute(routeName, clusterName)
	listener   = resource.MakeHTTPListener(resource.Ads, listenerName, 80, routeName)
	clusterV3  = resource.MakeV3Cluster(resource.Ads, clusterName)
	routeV3    = resource.MakeV3Route(routeName, clusterName)
	listenerV3 = resource.MakeV3HTTPListener(resource.Ads, listenerName, 80, routeName)
	testTypes  = map[apiversions.APIVersion][]string{
		apiversions.V2: []string{
			cache.EndpointType[apiversions.V2],
			cache.ClusterType[apiversions.V2],
			cache.RouteType[apiversions.V2],
			cache.ListenerType[apiversions.V2],
		},
		apiversions.V3: []string{
			//cache.EndpointType[apiversions.V3],
			cache.ClusterType[apiversions.V3],
			cache.RouteType[apiversions.V3],
			cache.ListenerType[apiversions.V3],
		},
	}
)

func makeResponses() map[string][]cache.Response {
	return map[string][]cache.Response{
		cache.EndpointType[apiversions.V2]: []cache.Response{{
			Version:    "1",
			Resources:  []cache.Resource{endpoint},
			APIVersion: apiversions.V2,
		}},
		cache.ClusterType[apiversions.V2]: []cache.Response{{
			Version:    "2",
			Resources:  []cache.Resource{cluster},
			APIVersion: apiversions.V2,
		}},
		cache.RouteType[apiversions.V2]: []cache.Response{{
			Version:    "3",
			Resources:  []cache.Resource{route},
			APIVersion: apiversions.V2,
		}},
		cache.ListenerType[apiversions.V2]: []cache.Response{{
			Version:    "4",
			Resources:  []cache.Resource{listener},
			APIVersion: apiversions.V2,
		}},
		cache.ClusterType[apiversions.V3]: []cache.Response{{
			Version:    "5",
			Resources:  []cache.Resource{clusterV3},
			APIVersion: apiversions.V3,
		}},
		cache.RouteType[apiversions.V3]: []cache.Response{{
			Version:    "6",
			Resources:  []cache.Resource{routeV3},
			APIVersion: apiversions.V3,
		}},
		cache.ListenerType[apiversions.V3]: []cache.Response{{
			Version:    "7",
			Resources:  []cache.Resource{listenerV3},
			APIVersion: apiversions.V3,
		}},
	}
}

func TestServerShutdown(t *testing.T) {
	for apiVersion, types := range testTypes {
		for _, typ := range types {
			t.Run(typ, func(t *testing.T) {
				config := makeMockConfigWatcher()
				config.responses = makeResponses()
				shutdown := make(chan bool)
				ctx, cancel := context.WithCancel(context.Background())
				s := server.NewServer(ctx, config, &callbacks{})

				// make a request
				resp := makeMockStream(t)
				switch apiVersion {
				case apiversions.V2:
					resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{Node: nodeV2}
				case apiversions.V3:
					resp.s3.recv <- &envoy_service_discovery_v3.DiscoveryRequest{Node: nodeV3}
				}
				go func() {
					var err error
					switch typ {
					case cache.EndpointType[apiversions.V2]:
						err = s.StreamEndpoints(resp.s2)
					case cache.ClusterType[apiversions.V2]:
						err = s.StreamClusters(resp.s2)
					case cache.RouteType[apiversions.V2]:
						err = s.StreamRoutes(resp.s2)
					case cache.ListenerType[apiversions.V2]:
						err = s.StreamListeners(resp.s2)
					case cache.EndpointType[apiversions.V3]:
						err = s.V3().StreamEndpoints(resp.s3)
					case cache.ClusterType[apiversions.V3]:
						err = s.V3().StreamClusters(resp.s3)
					case cache.RouteType[apiversions.V3]:
						err = s.V3().StreamRoutes(resp.s3)
					case cache.ListenerType[apiversions.V3]:
						err = s.V3().StreamListeners(resp.s3)
					}
					if err != nil {
						t.Errorf("Stream() => got %v, want no error", err)
					}
					shutdown <- true
				}()

				go func() {
					defer cancel()
				}()

				select {
				case <-shutdown:
				case <-time.After(1 * time.Second):
					t.Fatalf("got no response")
				}
			})
		}
	}
}

func TestResponseHandlers(t *testing.T) {
	for apiVersion, types := range testTypes {
		for _, typ := range types {
			t.Run(typ, func(t *testing.T) {
				config := makeMockConfigWatcher()
				config.responses = makeResponses()
				s := server.NewServer(context.Background(), config, &callbacks{})

				// make a request
				resp := makeMockStream(t)
				switch apiVersion {
				case apiversions.V2:
					resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{Node: nodeV2}
				case apiversions.V3:
					resp.s3.recv <- &envoy_service_discovery_v3.DiscoveryRequest{Node: nodeV3}
				}
				go func() {
					var err error
					switch typ {
					case cache.EndpointType[apiversions.V2]:
						err = s.StreamEndpoints(resp.s2)
					case cache.ClusterType[apiversions.V2]:
						err = s.StreamClusters(resp.s2)
					case cache.RouteType[apiversions.V2]:
						err = s.StreamRoutes(resp.s2)
					case cache.ListenerType[apiversions.V2]:
						err = s.StreamListeners(resp.s2)
					case cache.EndpointType[apiversions.V3]:
						err = s.V3().StreamEndpoints(resp.s3)
					case cache.ClusterType[apiversions.V3]:
						err = s.V3().StreamClusters(resp.s3)
					case cache.RouteType[apiversions.V3]:
						err = s.V3().StreamRoutes(resp.s3)
					case cache.ListenerType[apiversions.V3]:
						err = s.V3().StreamListeners(resp.s3)
					}
					if err != nil {
						t.Errorf("Stream() => got %v, want no error", err)
					}
				}()

				// check a response
				select {
				case <-resp.s2.sent:
					close(resp.s2.recv)
					if want := map[string]int{typ: 1}; !reflect.DeepEqual(want, config.counts) {
						t.Errorf("watch counts => got %v, want %v", config.counts, want)
					}
				case <-resp.s3.sent:
					close(resp.s3.recv)
					if want := map[string]int{typ: 1}; !reflect.DeepEqual(want, config.counts) {
						t.Errorf("watch counts => got %v, want %v", config.counts, want)
					}
				case <-time.After(1 * time.Second):
					t.Fatalf("got no response")
				}
			})
		}
	}
}

func TestFetch(t *testing.T) {
	config := makeMockConfigWatcher()
	config.responses = makeResponses()
	cb := &callbacks{}
	s := server.NewServer(context.Background(), config, cb)
	if out, err := s.FetchEndpoints(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out == nil || err != nil {
		t.Errorf("unexpected empty or error for endpoints: %v", err)
	}
	if out, err := s.FetchClusters(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out == nil || err != nil {
		t.Errorf("unexpected empty or error for clusters: %v", err)
	}
	if out, err := s.FetchRoutes(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out == nil || err != nil {
		t.Errorf("unexpected empty or error for routes: %v", err)
	}
	if out, err := s.FetchListeners(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out == nil || err != nil {
		t.Errorf("unexpected empty or error for listeners: %v", err)
	}

	// try again and expect empty results
	if out, err := s.FetchEndpoints(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out != nil {
		t.Errorf("expected empty or error for endpoints: %v", err)
	}
	if out, err := s.FetchClusters(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out != nil {
		t.Errorf("expected empty or error for clusters: %v", err)
	}
	if out, err := s.FetchRoutes(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out != nil {
		t.Errorf("expected empty or error for routes: %v", err)
	}
	if out, err := s.FetchListeners(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out != nil {
		t.Errorf("expected empty or error for listeners: %v", err)
	}

	// try empty requests: not valid in a real gRPC server
	if out, err := s.FetchEndpoints(context.Background(), nil); out != nil {
		t.Errorf("expected empty on empty request: %v", err)
	}
	if out, err := s.FetchClusters(context.Background(), nil); out != nil {
		t.Errorf("expected empty on empty request: %v", err)
	}
	if out, err := s.FetchRoutes(context.Background(), nil); out != nil {
		t.Errorf("expected empty on empty request: %v", err)
	}
	if out, err := s.FetchListeners(context.Background(), nil); out != nil {
		t.Errorf("expected empty on empty request: %v", err)
	}

	// send error from callback
	cb.callbackError = true
	if out, err := s.FetchEndpoints(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out != nil || err == nil {
		t.Errorf("expected empty or error due to callback error")
	}
	if out, err := s.FetchClusters(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out != nil || err == nil {
		t.Errorf("expected empty or error due to callback error")
	}
	if out, err := s.FetchRoutes(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out != nil || err == nil {
		t.Errorf("expected empty or error due to callback error")
	}
	if out, err := s.FetchListeners(context.Background(), &envoy_api_v2.DiscoveryRequest{Node: nodeV2}); out != nil || err == nil {
		t.Errorf("expected empty or error due to callback error")
	}

	// verify fetch callbacks
	if want := 8; cb.fetchReq != want {
		t.Errorf("unexpected number of fetch requests: got %d, want %d", cb.fetchReq, want)
	}
	if want := 4; cb.fetchResp != want {
		t.Errorf("unexpected number of fetch responses: got %d, want %d", cb.fetchResp, want)
	}
}

func TestWatchClosed(t *testing.T) {
	for apiVersion, types := range testTypes {
		for _, typ := range types {
			t.Run(typ, func(t *testing.T) {
				config := makeMockConfigWatcher()
				config.closeWatch = true
				s := server.NewServer(context.Background(), config, &callbacks{})

				// make a request
				resp := makeMockStream(t)
				switch apiVersion {
				case apiversions.V2:
					resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{
						Node:    nodeV2,
						TypeUrl: typ,
					}
					// check that response fails since watch gets closed
					if err := s.StreamAggregatedResources(resp.s2); err == nil {
						t.Error("Stream() => got no error, want watch failed")
					}
					close(resp.s2.recv)
				case apiversions.V3:
					resp.s3.recv <- &envoy_service_discovery_v3.DiscoveryRequest{
						Node:    nodeV3,
						TypeUrl: typ,
					}
					// check that response fails since watch gets closed
					if err := s.V3().StreamAggregatedResources(resp.s3); err == nil {
						t.Error("Stream() => got no error, want watch failed")
					}
					close(resp.s3.recv)
				}

			})
		}
	}
}

func TestSendError(t *testing.T) {
	for apiVersion, types := range testTypes {
		for _, typ := range types {
			t.Run(typ, func(t *testing.T) {
				config := makeMockConfigWatcher()
				config.responses = makeResponses()
				s := server.NewServer(context.Background(), config, &callbacks{})

				// make a request
				resp := makeMockStream(t)
				switch apiVersion {
				case apiversions.V2:
					resp.s2.sendError = true
					resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{
						Node:    nodeV2,
						TypeUrl: typ,
					}
					// check that response fails since send returns error
					if err := s.StreamAggregatedResources(resp.s2); err == nil {
						t.Error("Stream() => got no error, want send error")
					}
					close(resp.s2.recv)
				case apiversions.V3:
					resp.s3.sendError = true
					resp.s3.recv <- &envoy_service_discovery_v3.DiscoveryRequest{
						Node:    nodeV3,
						TypeUrl: typ,
					}
					// check that response fails since send returns error
					if err := s.V3().StreamAggregatedResources(resp.s3); err == nil {
						t.Error("Stream() => got no error, want send error")
					}
					close(resp.s3.recv)
				}
			})
		}
	}
}

func TestStaleNonce(t *testing.T) {
	for apiVersion, types := range testTypes {
		for _, typ := range types {
			t.Run(typ, func(t *testing.T) {
				config := makeMockConfigWatcher()
				config.responses = makeResponses()
				s := server.NewServer(context.Background(), config, &callbacks{})

				resp := makeMockStream(t)
				stop := make(chan struct{})
				switch apiVersion {
				case apiversions.V2:
					resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{
						Node:    nodeV2,
						TypeUrl: typ,
					}
					go func() {
						if err := s.StreamAggregatedResources(resp.s2); err != nil {
							t.Errorf("StreamAggregatedResources() => got %v, want no error", err)
						}
						// should be two watches called
						if want := map[string]int{typ: 2}; !reflect.DeepEqual(want, config.counts) {
							t.Errorf("watch counts => got %v, want %v", config.counts, want)
						}
						close(stop)
					}()
				case apiversions.V3:
					resp.s3.recv <- &envoy_service_discovery_v3.DiscoveryRequest{
						Node:    nodeV3,
						TypeUrl: typ,
					}
					go func() {
						if err := s.V3().StreamAggregatedResources(resp.s3); err != nil {
							t.Errorf("StreamAggregatedResources() => got %v, want no error", err)
						}
						// should be two watches called
						if want := map[string]int{typ: 2}; !reflect.DeepEqual(want, config.counts) {
							t.Errorf("watch counts => got %v, want %v", config.counts, want)
						}
						close(stop)
					}()
				}
				select {
				case <-resp.s2.sent:
					// stale request
					resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{
						Node:          nodeV2,
						TypeUrl:       typ,
						ResponseNonce: "xyz",
					}
					// fresh request
					resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{
						VersionInfo:   "1",
						Node:          nodeV2,
						TypeUrl:       typ,
						ResponseNonce: "1",
					}
					close(resp.s2.recv)
				case <-resp.s3.sent:
					// stale request
					resp.s3.recv <- &envoy_service_discovery_v3.DiscoveryRequest{
						Node:          nodeV3,
						TypeUrl:       typ,
						ResponseNonce: "xyz",
					}
					// fresh request
					resp.s3.recv <- &envoy_service_discovery_v3.DiscoveryRequest{
						VersionInfo:   "1",
						Node:          nodeV3,
						TypeUrl:       typ,
						ResponseNonce: "1",
					}
					close(resp.s3.recv)
				case <-time.After(1 * time.Second):
					t.Fatalf("got %d messages on the stream, not 4", resp.s2.nonce)
				}
				<-stop
			})
		}
	}
}

func TestAggregatedHandlers(t *testing.T) {
	config := makeMockConfigWatcher()
	config.responses = makeResponses()
	resp := makeMockStream(t)

	resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{
		Node:    nodeV2,
		TypeUrl: cache.ListenerType[apiversions.V2],
	}
	resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{
		Node:    nodeV2,
		TypeUrl: cache.ClusterType[apiversions.V2],
	}
	resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{
		Node:          nodeV2,
		TypeUrl:       cache.EndpointType[apiversions.V2],
		ResourceNames: []string{clusterName},
	}
	resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{
		Node:          nodeV2,
		TypeUrl:       cache.RouteType[apiversions.V2],
		ResourceNames: []string{routeName},
	}

	s := server.NewServer(context.Background(), config, &callbacks{})
	go func() {
		if err := s.StreamAggregatedResources(resp.s2); err != nil {
			t.Errorf("StreamAggregatedResources() => got %v, want no error", err)
		}
	}()

	count := 0
	for {
		select {
		case <-resp.s2.sent:
			count++
			if count >= 4 {
				close(resp.s2.recv)
				if want := map[string]int{
					cache.EndpointType[apiversions.V2]: 1,
					cache.ClusterType[apiversions.V2]:  1,
					cache.RouteType[apiversions.V2]:    1,
					cache.ListenerType[apiversions.V2]: 1,
				}; !reflect.DeepEqual(want, config.counts) {
					t.Errorf("watch counts => got %v, want %v", config.counts, want)
				}

				// got all messages
				return
			}
		case <-time.After(1 * time.Second):
			t.Fatalf("got %d messages on the stream, not 4", count)
		}
	}
}

func TestAggregateRequestType(t *testing.T) {
	config := makeMockConfigWatcher()
	s := server.NewServer(context.Background(), config, &callbacks{})
	resp := makeMockStream(t)
	resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{Node: nodeV2}
	if err := s.StreamAggregatedResources(resp.s2); err == nil {
		t.Error("StreamAggregatedResources() => got nil, want an error")
	}
}

func TestCallbackError(t *testing.T) {
	for apiVersion, types := range testTypes {
		for _, typ := range types {
			t.Run(typ, func(t *testing.T) {
				config := makeMockConfigWatcher()
				config.responses = makeResponses()
				s := server.NewServer(context.Background(), config, &callbacks{callbackError: true})

				// make a request
				resp := makeMockStream(t)
				switch apiVersion {
				case apiversions.V2:
					resp.s2.recv <- &envoy_api_v2.DiscoveryRequest{
						Node:    nodeV2,
						TypeUrl: typ,
					}
					// check that response fails since stream open returns error
					if err := s.StreamAggregatedResources(resp.s2); err == nil {
						t.Error("Stream() => got no error, want error")
					}
					close(resp.s2.recv)
				case apiversions.V3:
					resp.s3.recv <- &envoy_service_discovery_v3.DiscoveryRequest{
						Node:    nodeV3,
						TypeUrl: typ,
					}
					// check that response fails since stream open returns error
					if err := s.V3().StreamAggregatedResources(resp.s3); err == nil {
						t.Error("Stream() => got no error, want error")
					}
					close(resp.s3.recv)
				}
			})
		}
	}
}
