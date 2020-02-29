package server

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	envoy_api_v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	envoy_api_v2_core "github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	envoy_config_core_v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoy_service_discovery_v3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/apiversions"
)

func setNonce(d apiversions.DiscoveryResponse, n string) error {
	switch r := d.(type) {
	case *envoy_api_v2.DiscoveryResponse:
		r.Nonce = n
	case *envoy_service_discovery_v3.DiscoveryResponse:
		r.Nonce = n
	default:
		return errors.New("unsupported discovery type")
	}
	return nil
}

func setTypeURL(d apiversions.DiscoveryRequest, url string) error {
	switch r := d.(type) {
	case *envoy_api_v2.DiscoveryRequest:
		r.TypeUrl = url
	case *envoy_service_discovery_v3.DiscoveryRequest:
		r.TypeUrl = url
	default:
		return status.Errorf(codes.InvalidArgument, "invalid input")
	}
	return nil
}

type nodeCache struct {
	v2 *envoy_api_v2_core.Node
	v3 *envoy_config_core_v3.Node
}

func (n *nodeCache) update(req apiversions.DiscoveryRequest) {
	switch r := req.(type) {
	case *envoy_api_v2.DiscoveryRequest:
		if r.Node != nil {
			n.v2 = r.Node
		} else {
			r.Node = n.v2
		}
	case *envoy_service_discovery_v3.DiscoveryRequest:
		if r.Node != nil {
			n.v3 = r.Node
		} else {
			r.Node = n.v3
		}
	}
}

type stream struct {
	s2 streamV2
	s3 streamV3
}

func (s *stream) Send(d apiversions.DiscoveryResponse) error {
	switch r := d.(type) {
	case *envoy_api_v2.DiscoveryResponse:
		return s.s2.Send(r)
	case *envoy_service_discovery_v3.DiscoveryResponse:
		return s.s3.Send(r)
	default:
		return status.Errorf(codes.InvalidArgument, "unsupported response type")
	}
}

func (s *stream) Recv(v apiversions.APIVersion) (apiversions.DiscoveryRequest, error) {
	switch v {
	case apiversions.V2:
		return s.s2.Recv()
	case apiversions.V3:
		return s.s3.Recv()
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported api version")
	}
}

func (s *stream) Context() context.Context {
	switch {
	case s.s2 != nil:
		return s.s2.Context()
	case s.s3 != nil:
		return s.s3.Context()
	default:
		return context.Background()
	}
}
