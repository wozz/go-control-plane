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

// Package server provides an implementation of a streaming xDS server.
package server

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"

	"github.com/golang/protobuf/ptypes/any"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	envoy_api_v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	envoy_service_cluster_v3 "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	envoy_service_discovery_v2 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v2"
	envoy_service_discovery_v3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	envoy_service_endpoint_v3 "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	envoy_service_listener_v3 "github.com/envoyproxy/go-control-plane/envoy/service/listener/v3"
	envoy_service_route_v3 "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	envoy_service_runtime_v3 "github.com/envoyproxy/go-control-plane/envoy/service/runtime/v3"
	envoy_service_secret_v3 "github.com/envoyproxy/go-control-plane/envoy/service/secret/v3"
	"github.com/envoyproxy/go-control-plane/pkg/apiversions"
	"github.com/envoyproxy/go-control-plane/pkg/cache"
)

// Server is a collection of handlers for streaming discovery requests.
type Server interface {
	envoy_api_v2.EndpointDiscoveryServiceServer
	envoy_api_v2.ClusterDiscoveryServiceServer
	envoy_api_v2.RouteDiscoveryServiceServer
	envoy_api_v2.ListenerDiscoveryServiceServer
	envoy_service_discovery_v2.AggregatedDiscoveryServiceServer
	envoy_service_discovery_v2.SecretDiscoveryServiceServer
	envoy_service_discovery_v2.RuntimeDiscoveryServiceServer

	// Fetch is the universal fetch method.
	Fetch(context.Context, *envoy_api_v2.DiscoveryRequest) (*envoy_api_v2.DiscoveryResponse, error)

	V3() ServerV3

	// RegisterAll is a helper to register all supported grpc services
	RegisterAll(*grpc.Server)
}

var _ Server = (*server)(nil)

// Callbacks is a collection of callbacks inserted into the server operation.
// The callbacks are invoked synchronously.
type Callbacks interface {
	// OnStreamOpen is called once an xDS stream is open with a stream ID and the type URL (or "" for ADS).
	// Returning an error will end processing and close the stream. OnStreamClosed will still be called.
	OnStreamOpen(context.Context, int64, string) error
	// OnStreamClosed is called immediately prior to closing an xDS stream with a stream ID.
	OnStreamClosed(int64)
	// OnStreamRequest is called once a request is received on a stream.
	// Returning an error will end processing and close the stream. OnStreamClosed will still be called.
	OnStreamRequest(int64, apiversions.DiscoveryRequest) error
	// OnStreamResponse is called immediately prior to sending a response on a stream.
	OnStreamResponse(int64, apiversions.DiscoveryRequest, apiversions.DiscoveryResponse)
	// OnFetchRequest is called for each Fetch request. Returning an error will end processing of the
	// request and respond with an error.
	OnFetchRequest(context.Context, apiversions.DiscoveryRequest) error
	// OnFetchResponse is called immediately prior to sending a response.
	OnFetchResponse(apiversions.DiscoveryRequest, apiversions.DiscoveryResponse)
}

// NewServer creates handlers from a config watcher and callbacks.
func NewServer(ctx context.Context, config cache.Cache, callbacks Callbacks) Server {
	s := &server{
		cache:     config,
		callbacks: callbacks,
		ctx:       ctx,
	}
	sv3 := &serverV3{
		s: s,
	}
	s.v3 = sv3
	return s
}

type server struct {
	cache     cache.Cache
	callbacks Callbacks

	// streamCount for counting bi-di streams
	streamCount int64
	ctx         context.Context

	v3 ServerV3
}

type streamV2 interface {
	grpc.ServerStream

	Send(*envoy_api_v2.DiscoveryResponse) error
	Recv() (*envoy_api_v2.DiscoveryRequest, error)
}

// watches for all xDS resource types
type watches struct {
	endpoints chan cache.Response
	clusters  chan cache.Response
	routes    chan cache.Response
	listeners chan cache.Response
	secrets   chan cache.Response
	runtimes  chan cache.Response

	endpointCancel func()
	clusterCancel  func()
	routeCancel    func()
	listenerCancel func()
	secretCancel   func()
	runtimeCancel  func()

	endpointNonce string
	clusterNonce  string
	routeNonce    string
	listenerNonce string
	secretNonce   string
	runtimeNonce  string
}

// Cancel all watches
func (values watches) Cancel() {
	if values.endpointCancel != nil {
		values.endpointCancel()
	}
	if values.clusterCancel != nil {
		values.clusterCancel()
	}
	if values.routeCancel != nil {
		values.routeCancel()
	}
	if values.listenerCancel != nil {
		values.listenerCancel()
	}
	if values.secretCancel != nil {
		values.secretCancel()
	}
	if values.runtimeCancel != nil {
		values.runtimeCancel()
	}
}

func createResponse(resp *cache.Response, typeURL string, discoveryAPIVersion apiversions.APIVersion) (apiversions.DiscoveryResponse, error) {
	if resp == nil {
		return nil, errors.New("missing response")
	}

	var resources []*any.Any
	if resp.ResourceMarshaled {
		resources = make([]*any.Any, len(resp.MarshaledResources))
	} else {
		resources = make([]*any.Any, len(resp.Resources))
	}

	for i := 0; i < len(resources); i++ {
		// Envoy relies on serialized protobuf bytes for detecting changes to the resources.
		// This requires deterministic serialization.
		if resp.ResourceMarshaled {
			resources[i] = &any.Any{
				TypeUrl: typeURL,
				Value:   resp.MarshaledResources[i],
			}
		} else {
			marshaledResource, err := cache.MarshalResource(resp.Resources[i])
			if err != nil {
				return nil, err
			}

			resources[i] = &any.Any{
				TypeUrl: typeURL,
				Value:   marshaledResource,
			}
		}
	}
	var out apiversions.DiscoveryResponse
	switch discoveryAPIVersion {
	case apiversions.V2:
		out = &envoy_api_v2.DiscoveryResponse{
			VersionInfo: resp.Version,
			Resources:   resources,
			TypeUrl:     typeURL,
		}
	case apiversions.V3:
		out = &envoy_service_discovery_v3.DiscoveryResponse{
			VersionInfo: resp.Version,
			Resources:   resources,
			TypeUrl:     typeURL,
		}
	default:
		return nil, errors.New("unknown api version in cache")
	}
	return out, nil
}

// process handles a bi-di stream request
func (s *server) process(stream *stream, reqCh <-chan apiversions.DiscoveryRequest, defaultTypeURL string, discoveryAPIVersion apiversions.APIVersion) error {
	// increment stream count
	streamID := atomic.AddInt64(&s.streamCount, 1)

	// unique nonce generator for req-resp pairs per xDS stream; the server
	// ignores stale nonces. nonce is only modified within send() function.
	var streamNonce int64

	// a collection of watches per request type
	var values watches
	defer func() {
		values.Cancel()
		if s.callbacks != nil {
			s.callbacks.OnStreamClosed(streamID)
		}
	}()

	// sends a response by serializing to protobuf Any
	send := func(resp cache.Response, typeURL string) (string, error) {
		out, err := createResponse(&resp, typeURL, discoveryAPIVersion)
		if err != nil {
			return "", err
		}

		// increment nonce
		streamNonce = streamNonce + 1
		if err := setNonce(out, strconv.FormatInt(streamNonce, 10)); err != nil {
			return "", err
		}
		if s.callbacks != nil {
			s.callbacks.OnStreamResponse(streamID, resp.Request, out)
		}
		return out.GetNonce(), stream.Send(out)
	}

	if s.callbacks != nil {
		if err := s.callbacks.OnStreamOpen(stream.Context(), streamID, defaultTypeURL); err != nil {
			return err
		}
	}

	// node may only be set on the first discovery request
	node := &nodeCache{}

	for {
		select {
		case <-s.ctx.Done():
			return nil
		// config watcher can send the requested resources types in any order
		case resp, more := <-values.endpoints:
			if !more {
				return status.Errorf(codes.Unavailable, "endpoints watch failed")
			}
			nonce, err := send(resp, cache.EndpointType[resp.APIVersion])
			if err != nil {
				return err
			}
			values.endpointNonce = nonce

		case resp, more := <-values.clusters:
			if !more {
				return status.Errorf(codes.Unavailable, "clusters watch failed")
			}
			nonce, err := send(resp, cache.ClusterType[resp.APIVersion])
			if err != nil {
				return err
			}
			values.clusterNonce = nonce

		case resp, more := <-values.routes:
			if !more {
				return status.Errorf(codes.Unavailable, "routes watch failed")
			}
			nonce, err := send(resp, cache.RouteType[resp.APIVersion])
			if err != nil {
				return err
			}
			values.routeNonce = nonce

		case resp, more := <-values.listeners:
			if !more {
				return status.Errorf(codes.Unavailable, "listeners watch failed")
			}
			nonce, err := send(resp, cache.ListenerType[resp.APIVersion])
			if err != nil {
				return err
			}
			values.listenerNonce = nonce

		case resp, more := <-values.secrets:
			if !more {
				return status.Errorf(codes.Unavailable, "secrets watch failed")
			}
			nonce, err := send(resp, cache.SecretType[resp.APIVersion])
			if err != nil {
				return err
			}
			values.secretNonce = nonce

		case resp, more := <-values.runtimes:
			if !more {
				return status.Errorf(codes.Unavailable, "runtimes watch failed")
			}
			nonce, err := send(resp, cache.RuntimeType[resp.APIVersion])
			if err != nil {
				return err
			}
			values.runtimeNonce = nonce

		case req, more := <-reqCh:
			// input stream ended or errored out
			if !more {
				return nil
			}
			if req == nil {
				return status.Errorf(codes.Unavailable, "empty request")
			}

			// node field in discovery request is delta-compressed
			node.update(req)

			// nonces can be reused across streams; we verify nonce only if nonce is not initialized
			nonce := req.GetResponseNonce()

			// type URL is required for ADS but is implicit for xDS
			if defaultTypeURL == cache.AnyType {
				if req.GetTypeUrl() == "" {
					return status.Errorf(codes.InvalidArgument, "type URL is required for ADS")
				}
			} else if req.GetTypeUrl() == "" {
				if err := setTypeURL(req, defaultTypeURL); err != nil {
					return err
				}
			}

			if s.callbacks != nil {
				if err := s.callbacks.OnStreamRequest(streamID, req); err != nil {
					return err
				}
			}

			// cancel existing watches to (re-)request a newer version
			switch {
			case cache.EndpointType.Includes(req.GetTypeUrl()) && (values.endpointNonce == "" || values.endpointNonce == nonce):
				if values.endpointCancel != nil {
					values.endpointCancel()
				}
				values.endpoints, values.endpointCancel = s.cache.CreateWatch(req)
			case cache.ClusterType.Includes(req.GetTypeUrl()) && (values.clusterNonce == "" || values.clusterNonce == nonce):
				if values.clusterCancel != nil {
					values.clusterCancel()
				}
				values.clusters, values.clusterCancel = s.cache.CreateWatch(req)
			case cache.RouteType.Includes(req.GetTypeUrl()) && (values.routeNonce == "" || values.routeNonce == nonce):
				if values.routeCancel != nil {
					values.routeCancel()
				}
				values.routes, values.routeCancel = s.cache.CreateWatch(req)
			case cache.ListenerType.Includes(req.GetTypeUrl()) && (values.listenerNonce == "" || values.listenerNonce == nonce):
				if values.listenerCancel != nil {
					values.listenerCancel()
				}
				values.listeners, values.listenerCancel = s.cache.CreateWatch(req)
			case cache.SecretType.Includes(req.GetTypeUrl()) && (values.secretNonce == "" || values.secretNonce == nonce):
				if values.secretCancel != nil {
					values.secretCancel()
				}
				values.secrets, values.secretCancel = s.cache.CreateWatch(req)
			case cache.RuntimeType.Includes(req.GetTypeUrl()) && (values.runtimeNonce == "" || values.runtimeNonce == nonce):
				if values.runtimeCancel != nil {
					values.runtimeCancel()
				}
				values.runtimes, values.runtimeCancel = s.cache.CreateWatch(req)
			}
		}
	}
}

// handlerV2 converts a blocking read call to channels and initiates stream processing
func (s *server) handlerV2(s2 streamV2, typeURL string) error {
	// a channel for receiving incoming requests
	reqCh := make(chan apiversions.DiscoveryRequest)
	reqStop := int32(0)
	st := &stream{
		s2: s2,
	}
	go func() {
		for {
			req, err := st.Recv(apiversions.V2)
			if atomic.LoadInt32(&reqStop) != 0 {
				return
			}
			if err != nil {
				close(reqCh)
				return
			}
			reqCh <- req
		}
	}()

	err := s.process(st, reqCh, typeURL, apiversions.V2)

	// prevents writing to a closed channel if send failed on blocked recv
	// TODO(kuat) figure out how to unblock recv through gRPC API
	atomic.StoreInt32(&reqStop, 1)

	return err
}

func (s *server) StreamAggregatedResources(stream envoy_service_discovery_v2.AggregatedDiscoveryService_StreamAggregatedResourcesServer) error {
	return s.handlerV2(stream, cache.AnyType)
}

func (s *server) StreamEndpoints(stream envoy_api_v2.EndpointDiscoveryService_StreamEndpointsServer) error {
	return s.handlerV2(stream, cache.EndpointType[apiversions.V2])
}

func (s *server) StreamClusters(stream envoy_api_v2.ClusterDiscoveryService_StreamClustersServer) error {
	return s.handlerV2(stream, cache.ClusterType[apiversions.V2])
}

func (s *server) StreamRoutes(stream envoy_api_v2.RouteDiscoveryService_StreamRoutesServer) error {
	return s.handlerV2(stream, cache.RouteType[apiversions.V2])
}

func (s *server) StreamListeners(stream envoy_api_v2.ListenerDiscoveryService_StreamListenersServer) error {
	return s.handlerV2(stream, cache.ListenerType[apiversions.V2])
}

func (s *server) StreamSecrets(stream envoy_service_discovery_v2.SecretDiscoveryService_StreamSecretsServer) error {
	return s.handlerV2(stream, cache.SecretType[apiversions.V2])
}

func (s *server) StreamRuntime(stream envoy_service_discovery_v2.RuntimeDiscoveryService_StreamRuntimeServer) error {
	return s.handlerV2(stream, cache.RuntimeType[apiversions.V2])
}

// Fetch is the universal fetch method.
func (s *server) Fetch(ctx context.Context, req *envoy_api_v2.DiscoveryRequest) (*envoy_api_v2.DiscoveryResponse, error) {
	if s.callbacks != nil {
		if err := s.callbacks.OnFetchRequest(ctx, req); err != nil {
			return nil, err
		}
	}
	resp, err := s.cache.Fetch(ctx, req)
	if err != nil {
		return nil, err
	}
	out, err := createResponse(resp, req.GetTypeUrl(), apiversions.V2)
	if s.callbacks != nil {
		s.callbacks.OnFetchResponse(req, out)
	}
	if err != nil {
		return nil, err
	}
	typedOut, ok := out.(*envoy_api_v2.DiscoveryResponse)
	if !ok {
		return nil, errors.New("internal error, invalid type for api version")
	}
	return typedOut, nil
}

func (s *server) FetchEndpoints(ctx context.Context, req *envoy_api_v2.DiscoveryRequest) (*envoy_api_v2.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.EndpointType[apiversions.V2]
	return s.Fetch(ctx, req)
}

func (s *server) FetchClusters(ctx context.Context, req *envoy_api_v2.DiscoveryRequest) (*envoy_api_v2.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.ClusterType[apiversions.V2]
	return s.Fetch(ctx, req)
}

func (s *server) FetchRoutes(ctx context.Context, req *envoy_api_v2.DiscoveryRequest) (*envoy_api_v2.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.RouteType[apiversions.V2]
	return s.Fetch(ctx, req)
}

func (s *server) FetchListeners(ctx context.Context, req *envoy_api_v2.DiscoveryRequest) (*envoy_api_v2.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.ListenerType[apiversions.V2]
	return s.Fetch(ctx, req)
}

func (s *server) FetchSecrets(ctx context.Context, req *envoy_api_v2.DiscoveryRequest) (*envoy_api_v2.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.SecretType[apiversions.V2]
	return s.Fetch(ctx, req)
}

func (s *server) FetchRuntime(ctx context.Context, req *envoy_api_v2.DiscoveryRequest) (*envoy_api_v2.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.RuntimeType[apiversions.V2]
	return s.Fetch(ctx, req)
}

func (s *server) DeltaAggregatedResources(_ envoy_service_discovery_v2.AggregatedDiscoveryService_DeltaAggregatedResourcesServer) error {
	return errors.New("not implemented")
}

func (s *server) DeltaEndpoints(_ envoy_api_v2.EndpointDiscoveryService_DeltaEndpointsServer) error {
	return errors.New("not implemented")
}

func (s *server) DeltaClusters(_ envoy_api_v2.ClusterDiscoveryService_DeltaClustersServer) error {
	return errors.New("not implemented")
}

func (s *server) DeltaRoutes(_ envoy_api_v2.RouteDiscoveryService_DeltaRoutesServer) error {
	return errors.New("not implemented")
}

func (s *server) DeltaListeners(_ envoy_api_v2.ListenerDiscoveryService_DeltaListenersServer) error {
	return errors.New("not implemented")
}

func (s *server) DeltaSecrets(_ envoy_service_discovery_v2.SecretDiscoveryService_DeltaSecretsServer) error {
	return errors.New("not implemented")
}

func (s *server) DeltaRuntime(_ envoy_service_discovery_v2.RuntimeDiscoveryService_DeltaRuntimeServer) error {
	return errors.New("not implemented")
}

func (s *server) V3() ServerV3 {
	return s.v3
}

func (s *server) RegisterAll(grpcServer *grpc.Server) {
	// v2 APIs
	envoy_service_discovery_v2.RegisterAggregatedDiscoveryServiceServer(grpcServer, s)
	envoy_api_v2.RegisterEndpointDiscoveryServiceServer(grpcServer, s)
	envoy_api_v2.RegisterClusterDiscoveryServiceServer(grpcServer, s)
	envoy_api_v2.RegisterRouteDiscoveryServiceServer(grpcServer, s)
	envoy_api_v2.RegisterListenerDiscoveryServiceServer(grpcServer, s)
	envoy_service_discovery_v2.RegisterSecretDiscoveryServiceServer(grpcServer, s)
	envoy_service_discovery_v2.RegisterRuntimeDiscoveryServiceServer(grpcServer, s)

	// v3 APIs
	envoy_service_discovery_v3.RegisterAggregatedDiscoveryServiceServer(grpcServer, s.V3())
	envoy_service_endpoint_v3.RegisterEndpointDiscoveryServiceServer(grpcServer, s.V3())
	envoy_service_cluster_v3.RegisterClusterDiscoveryServiceServer(grpcServer, s.V3())
	envoy_service_route_v3.RegisterRouteDiscoveryServiceServer(grpcServer, s.V3())
	envoy_service_listener_v3.RegisterListenerDiscoveryServiceServer(grpcServer, s.V3())
	envoy_service_secret_v3.RegisterSecretDiscoveryServiceServer(grpcServer, s.V3())
	envoy_service_runtime_v3.RegisterRuntimeDiscoveryServiceServer(grpcServer, s.V3())
}
