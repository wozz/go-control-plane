package server

import (
	"context"
	"errors"
	"sync/atomic"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	envoy_service_cluster_v3 "github.com/envoyproxy/go-control-plane/envoy/service/cluster/v3"
	envoy_service_discovery_v3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	envoy_service_endpoint_v3 "github.com/envoyproxy/go-control-plane/envoy/service/endpoint/v3"
	envoy_service_listener_v3 "github.com/envoyproxy/go-control-plane/envoy/service/listener/v3"
	envoy_service_route_v3 "github.com/envoyproxy/go-control-plane/envoy/service/route/v3"
	envoy_service_runtime_v3 "github.com/envoyproxy/go-control-plane/envoy/service/runtime/v3"
	envoy_service_secret_v3 "github.com/envoyproxy/go-control-plane/envoy/service/secret/v3"
	"github.com/envoyproxy/go-control-plane/pkg/apiversions"
	"github.com/envoyproxy/go-control-plane/pkg/cache"
)

// ServerV3 is a collection of handlers to support streaming v3 discovery requests
type ServerV3 interface {
	envoy_service_endpoint_v3.EndpointDiscoveryServiceServer
	envoy_service_cluster_v3.ClusterDiscoveryServiceServer
	envoy_service_route_v3.RouteDiscoveryServiceServer
	envoy_service_listener_v3.ListenerDiscoveryServiceServer
	envoy_service_discovery_v3.AggregatedDiscoveryServiceServer
	envoy_service_secret_v3.SecretDiscoveryServiceServer
	envoy_service_runtime_v3.RuntimeDiscoveryServiceServer

	// Fetch is the universal fetch method.
	Fetch(context.Context, *envoy_service_discovery_v3.DiscoveryRequest) (*envoy_service_discovery_v3.DiscoveryResponse, error)
}

var _ ServerV3 = (*serverV3)(nil)

type serverV3 struct {
	s *server

	envoy_service_endpoint_v3.UnimplementedEndpointDiscoveryServiceServer
	envoy_service_cluster_v3.UnimplementedClusterDiscoveryServiceServer
	envoy_service_route_v3.UnimplementedRouteDiscoveryServiceServer
	envoy_service_listener_v3.UnimplementedListenerDiscoveryServiceServer
	envoy_service_discovery_v3.UnimplementedAggregatedDiscoveryServiceServer
	envoy_service_secret_v3.UnimplementedSecretDiscoveryServiceServer
	envoy_service_runtime_v3.UnimplementedRuntimeDiscoveryServiceServer
}

// handlerV3 converts a blocking read call to channels and initiates stream processing
func (s *serverV3) handlerV3(s3 streamV3, typeURL string) error {
	// a channel for receiving incoming requests
	reqCh := make(chan apiversions.DiscoveryRequest)
	reqStop := int32(0)
	st := &stream{
		s3: s3,
	}
	go func() {
		for {
			req, err := st.Recv(apiversions.V3)
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

	err := s.s.process(st, reqCh, typeURL, apiversions.V3)

	// prevents writing to a closed channel if send failed on blocked recv
	// TODO(kuat) figure out how to unblock recv through gRPC API
	atomic.StoreInt32(&reqStop, 1)

	return err
}

// Fetch is the universal fetch method.
func (s *serverV3) Fetch(ctx context.Context, req *envoy_service_discovery_v3.DiscoveryRequest) (*envoy_service_discovery_v3.DiscoveryResponse, error) {
	if s.s.callbacks != nil {
		if err := s.s.callbacks.OnFetchRequest(ctx, req); err != nil {
			return nil, err
		}
	}
	resp, err := s.s.cache.Fetch(ctx, req)
	if err != nil {
		return nil, err
	}
	out, err := createResponse(resp, req.GetTypeUrl(), apiversions.V3)
	if s.s.callbacks != nil {
		s.s.callbacks.OnFetchResponse(req, out)
	}
	if err != nil {
		return nil, err
	}
	typedOut, ok := out.(*envoy_service_discovery_v3.DiscoveryResponse)
	if !ok {
		return nil, errors.New("internal error, invalid type for api version")
	}
	return typedOut, nil
}

type streamV3 interface {
	grpc.ServerStream

	Send(*envoy_service_discovery_v3.DiscoveryResponse) error
	Recv() (*envoy_service_discovery_v3.DiscoveryRequest, error)
}

func (s *serverV3) StreamAggregatedResources(stream envoy_service_discovery_v3.AggregatedDiscoveryService_StreamAggregatedResourcesServer) error {
	return s.handlerV3(stream, cache.AnyType)
}

func (s *serverV3) StreamEndpoints(stream envoy_service_endpoint_v3.EndpointDiscoveryService_StreamEndpointsServer) error {
	return s.handlerV3(stream, cache.EndpointType[apiversions.V3])
}

func (s *serverV3) StreamClusters(stream envoy_service_cluster_v3.ClusterDiscoveryService_StreamClustersServer) error {
	return s.handlerV3(stream, cache.ClusterType[apiversions.V3])
}

func (s *serverV3) StreamRoutes(stream envoy_service_route_v3.RouteDiscoveryService_StreamRoutesServer) error {
	return s.handlerV3(stream, cache.RouteType[apiversions.V3])
}

func (s *serverV3) StreamListeners(stream envoy_service_listener_v3.ListenerDiscoveryService_StreamListenersServer) error {
	return s.handlerV3(stream, cache.ListenerType[apiversions.V3])
}

func (s *serverV3) StreamSecrets(stream envoy_service_secret_v3.SecretDiscoveryService_StreamSecretsServer) error {
	return s.handlerV3(stream, cache.SecretType[apiversions.V3])
}

func (s *serverV3) StreamRuntime(stream envoy_service_runtime_v3.RuntimeDiscoveryService_StreamRuntimeServer) error {
	return s.handlerV3(stream, cache.RuntimeType[apiversions.V3])
}

func (s *serverV3) FetchEndpoints(ctx context.Context, req *envoy_service_discovery_v3.DiscoveryRequest) (*envoy_service_discovery_v3.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.EndpointType[apiversions.V3]
	return s.Fetch(ctx, req)
}

func (s *serverV3) FetchClusters(ctx context.Context, req *envoy_service_discovery_v3.DiscoveryRequest) (*envoy_service_discovery_v3.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.ClusterType[apiversions.V3]
	return s.Fetch(ctx, req)
}

func (s *serverV3) FetchRoutes(ctx context.Context, req *envoy_service_discovery_v3.DiscoveryRequest) (*envoy_service_discovery_v3.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.RouteType[apiversions.V3]
	return s.Fetch(ctx, req)
}

func (s *serverV3) FetchListeners(ctx context.Context, req *envoy_service_discovery_v3.DiscoveryRequest) (*envoy_service_discovery_v3.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.ListenerType[apiversions.V3]
	return s.Fetch(ctx, req)
}

func (s *serverV3) FetchSecrets(ctx context.Context, req *envoy_service_discovery_v3.DiscoveryRequest) (*envoy_service_discovery_v3.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.SecretType[apiversions.V3]
	return s.Fetch(ctx, req)
}

func (s *serverV3) FetchRuntime(ctx context.Context, req *envoy_service_discovery_v3.DiscoveryRequest) (*envoy_service_discovery_v3.DiscoveryResponse, error) {
	if req == nil {
		return nil, status.Errorf(codes.Unavailable, "empty request")
	}
	req.TypeUrl = cache.RuntimeType[apiversions.V3]
	return s.Fetch(ctx, req)
}
