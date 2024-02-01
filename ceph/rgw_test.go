//   Copyright 2022 DigitalOcean
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

package ceph

import (
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestRGWGC(t *testing.T) {
	for _, tt := range []struct {
		input     []byte
		version   string
		reMatch   []*regexp.Regexp
		reUnmatch []*regexp.Regexp
	}{
		{
			input: []byte(`
[
   {
       "tag": "00000000-0001-0000-0000-9ec86fa9a561.9695966.3129536\u0000",
       "time": "1975-01-01 16:31:09.0.564455s",
       "objs": [
           {
               "pool": "pool.rgw.buckets.data",
               "oid": "12345678-0001-0000-0000-000000000000.123456.1100__shadow_.tNcmQWnIAlJMd33ZIdhnLF9HoaY9TOv_1",
               "key": "",
               "instance": ""
           },
           {
               "pool": "pool.rgw.buckets.data",
               "oid": "12345678-0002-0000-0000-000000000000.123456.1100__shadow_.tNcmQWnIAlJMd33ZIdhnLF9HoaY9TOv_1",
               "key": "",
               "instance": ""
           }
       ]
	},
   	{
       "tag": "00000000-0002-0000-0000-9ec86fa9a561.9695966.3129536\u0000",
       "time": "1975-01-01 17:31:09.0.564455s",
       "objs": [
           {
               "pool": "pool.rgw.buckets.data",
               "oid": "12345678-0004-0000-0000-000000000000.123456.1100__shadow_.tNcmQWnIAlJMd33ZIdhnLF9HoaY9TOv_1",
               "key": "",
               "instance": ""
           },
           {
               "pool": "pool.rgw.buckets.data",
               "oid": "12345678-0005-0000-0000-000000000000.123456.1100__shadow_.tNcmQWnIAlJMd33ZIdhnLF9HoaY9TOv_1",
               "key": "",
               "instance": ""
           }
       ]
	},
   	{
       "tag": "00000000-0002-0000-0000-9ec86fa9a561.9695966.3129536\u0000",
       "time": "3075-01-01 11:30:09.0.123456s",
       "objs": [
           {
               "pool": "pool.rgw.buckets.data",
               "oid": "12345678-0001-5555-0000-000000000000.123456.1100__shadow_.tNcmQWnIAlJMd33ZIdhnLF9HoaY9TOv_1",
               "key": "",
               "instance": ""
           },
           {
               "pool": "pool.rgw.buckets.data",
               "oid": "12345678-0002-5555-0000-000000000000.123456.1100__shadow_.tNcmQWnIAlJMd33ZIdhnLF9HoaY9TOv_1",
               "key": "",
               "instance": ""
           },
           {
               "pool": "pool.rgw.buckets.data",
               "oid": "12345678-0003-5555-0000-000000000000.123456.1100__shadow_.tNcmQWnIAlJMd33ZIdhnLF9HoaY9TOv_1",
               "key": "",
               "instance": ""
           }
       ]
	}
]
`),
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`ceph_rgw_gc_active_tasks{cluster="ceph"} 2`),
				regexp.MustCompile(`ceph_rgw_gc_active_objects{cluster="ceph"} 4`),
				regexp.MustCompile(`ceph_rgw_gc_pending_tasks{cluster="ceph"} 1`),
				regexp.MustCompile(`ceph_rgw_gc_pending_objects{cluster="ceph"} 3`),
			},
		},
		{
			input:   []byte(`[]`),
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`ceph_rgw_gc_active_tasks{cluster="ceph"} 0`),
				regexp.MustCompile(`ceph_rgw_gc_active_objects{cluster="ceph"} 0`),
				regexp.MustCompile(`ceph_rgw_gc_pending_tasks{cluster="ceph"} 0`),
				regexp.MustCompile(`ceph_rgw_gc_pending_objects{cluster="ceph"} 0`),
			},
		},
		{
			// force an error return json deserialization
			input:   []byte(`[ { "bad-object": 17,,, ]`),
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reUnmatch: []*regexp.Regexp{
				regexp.MustCompile(`ceph_rgw_gc`),
			},
		},
		{
			// force an error return from getRGWGCTaskList
			input:   nil,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reUnmatch: []*regexp.Regexp{
				regexp.MustCompile(`ceph_rgw_gc`),
			},
		},
	} {
		func() {
			conn := setupVersionMocks(tt.version, "{}")

			e := &Exporter{Conn: conn, Cluster: "ceph", Logger: logrus.New()}
			e.cc = map[string]versionedCollector{
				"rgw": NewRGWCollector(e, false),
			}

			e.cc["rgw"].(*RGWCollector).getRGWGCTaskList = func(cluster string, user string) ([]byte, error) {
				if tt.input != nil {
					return tt.input, nil
				}
				return nil, errors.New("fake error")
			}

			err := prometheus.Register(e)
			require.NoError(t, err)
			defer prometheus.Unregister(e)

			server := httptest.NewServer(promhttp.Handler())
			defer server.Close()

			resp, err := http.Get(server.URL)
			require.NoError(t, err)
			defer resp.Body.Close()

			buf, err := ioutil.ReadAll(resp.Body)
			require.NoError(t, err)

			for _, re := range tt.reMatch {
				require.True(t, re.Match(buf))
			}

			for _, re := range tt.reUnmatch {
				require.False(t, re.Match(buf))
			}
		}()
	}
}

func TestRGWReshardStats(t *testing.T) {
	for _, tt := range []struct {
		input     []byte
		version   string
		reMatch   []*regexp.Regexp
		reUnmatch []*regexp.Regexp
	}{
		{
			input: []byte(`
[
	{
		"time": "2024-02-01 09:42:10.905080Z",
		"tenant": "",
		"bucket_name": "bucket-1",
		"bucket_id": "97c1cfac-009f-4f7d-8d9d-9097c322c606.51988974.133",
		"new_instance_id": "",
		"old_num_shards": 3,
		"new_num_shards": 12
	}
]
`),
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`ceph_rgw_active_reshards{cluster="ceph"} 1`),
				regexp.MustCompile(`ceph_rgw_bucket_reshard{bucket="bucket-1",cluster="ceph"} 1`),
			},
		},
		{
			input:   []byte(`[]`),
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`ceph_rgw_active_reshards{cluster="ceph"} 0`),
			},
		},
	} {
		func() {
			conn := setupVersionMocks(tt.version, "{}")

			e := &Exporter{Conn: conn, Cluster: "ceph", Logger: logrus.New()}
			e.cc = map[string]versionedCollector{
				"rgw": NewRGWCollector(e, false),
			}

			e.cc["rgw"].(*RGWCollector).getRGWGCTaskList = func(cluster, user string) ([]byte, error) {
				return []byte(`[]`), nil
			}

			e.cc["rgw"].(*RGWCollector).getRGWReshardList = func(cluster, user string) ([]byte, error) {
				if tt.input != nil {
					return tt.input, nil
				}
				return nil, errors.New("fake error")
			}

			err := prometheus.Register(e)
			require.NoError(t, err)
			defer prometheus.Unregister(e)

			server := httptest.NewServer(promhttp.Handler())
			defer server.Close()

			resp, err := http.Get(server.URL)
			require.NoError(t, err)
			defer resp.Body.Close()

			buf, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			for _, re := range tt.reMatch {
				require.True(t, re.Match(buf), string(buf))
			}

			for _, re := range tt.reUnmatch {
				require.False(t, re.Match(buf))
			}
		}()
	}
}
