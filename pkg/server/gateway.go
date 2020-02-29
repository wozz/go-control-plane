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

package server

import (
	"bytes"
	"io/ioutil"
	"net/http"
	"path"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"

	v2 "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	v3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	"github.com/envoyproxy/go-control-plane/pkg/apiversions"
	"github.com/envoyproxy/go-control-plane/pkg/cache"
	"github.com/envoyproxy/go-control-plane/pkg/log"
)

// HTTPGateway is a custom implementation of [gRPC gateway](https://github.com/grpc-ecosystem/grpc-gateway)
// specialized to Envoy xDS API.
type HTTPGateway struct {
	// Log is an optional log for errors in response write
	Log log.Logger

	// Server is the underlying gRPC server
	Server Server
}

type discoveryType struct {
	typeURL    cache.DiscoveryType
	apiVersion apiversions.APIVersion
}

var discoveryTypeMap = map[string]discoveryType{
	"/v2/discovery:endpoints": discoveryType{cache.EndpointType, apiversions.V2},
	"/v2/discovery:clusters":  discoveryType{cache.ClusterType, apiversions.V2},
	"/v2/discovery:listeners": discoveryType{cache.ListenerType, apiversions.V2},
	"/v2/discovery:routes":    discoveryType{cache.RouteType, apiversions.V2},
	"/v2/discovery:secrets":   discoveryType{cache.SecretType, apiversions.V2},
	"/v2/discovery:runtime":   discoveryType{cache.RuntimeType, apiversions.V2},

	"/v3/discovery:endpoints": discoveryType{cache.EndpointType, apiversions.V3},
	"/v3/discovery:clusters":  discoveryType{cache.ClusterType, apiversions.V3},
	"/v3/discovery:listeners": discoveryType{cache.ListenerType, apiversions.V3},
	"/v3/discovery:routes":    discoveryType{cache.RouteType, apiversions.V3},
	"/v3/discovery:secrets":   discoveryType{cache.SecretType, apiversions.V3},
	"/v3/discovery:runtime":   discoveryType{cache.RuntimeType, apiversions.V3},
}

func (h *HTTPGateway) ServeHTTP(resp http.ResponseWriter, req *http.Request) {
	p := path.Clean(req.URL.Path)

	discType, found := discoveryTypeMap[p]
	if !found {
		http.Error(resp, "no endpoint", http.StatusNotFound)
		return
	}

	if req.Body == nil {
		http.Error(resp, "empty body", http.StatusBadRequest)
		return
	}

	body, err := ioutil.ReadAll(req.Body)
	if err != nil {
		http.Error(resp, "cannot read body", http.StatusBadRequest)
		return
	}

	var res proto.Message
	switch discType.apiVersion {
	case apiversions.V2:
		// parse as JSON
		out := &v2.DiscoveryRequest{}
		err = jsonpb.UnmarshalString(string(body), out)
		if err != nil {
			http.Error(resp, "cannot parse JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if out.TypeUrl == "" {
			out.TypeUrl = discType.typeURL[discType.apiVersion]
		} else if !discType.typeURL.Includes(out.TypeUrl) {
			http.Error(resp, "invalid resource type for api route", http.StatusBadRequest)
			return
		}
		res, err = h.Server.Fetch(req.Context(), out)
	case apiversions.V3:
		// parse as JSON
		out := &v3.DiscoveryRequest{}
		err = jsonpb.UnmarshalString(string(body), out)
		if err != nil {
			http.Error(resp, "cannot parse JSON body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if out.TypeUrl == "" {
			out.TypeUrl = discType.typeURL[discType.apiVersion]
		} else if !discType.typeURL.Includes(out.TypeUrl) {
			http.Error(resp, "invalid resource type for api route", http.StatusBadRequest)
			return
		}
		res, err = h.Server.V3().Fetch(req.Context(), out)
	}
	if err != nil {
		// SkipFetchErrors will return a 304 which will signify to the envoy client that
		// it is already at the latest version; all other errors will 500 with a message.
		if _, ok := err.(*cache.SkipFetchError); ok {
			resp.WriteHeader(http.StatusNotModified)
		} else {
			http.Error(resp, "fetch error: "+err.Error(), http.StatusInternalServerError)
		}
		return
	}

	buf := &bytes.Buffer{}
	if err := (&jsonpb.Marshaler{OrigName: true}).Marshal(buf, res); err != nil {
		http.Error(resp, "marshal error: "+err.Error(), http.StatusInternalServerError)
	}

	if _, err = resp.Write(buf.Bytes()); err != nil && h.Log != nil {
		h.Log.Errorf("gateway error: %v", err)
	}
}
