//   Copyright 2024 DigitalOcean
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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPoolUsageCollector(t *testing.T) {
	for _, tt := range []struct {
		input              string
		version            string
		reMatch, reUnmatch []*regexp.Regexp
	}{
		{
			input: `
{"pools": [
	{"name": "rbd", "id": 11, "stats": {"stored": 20, "objects": 5, "rd": 4, "wr": 6}}
]}`,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`pool_used_bytes{cluster="ceph",pool="rbd"} 20`),
				regexp.MustCompile(`pool_objects_total{cluster="ceph",pool="rbd"} 5`),
				regexp.MustCompile(`pool_read_total{cluster="ceph",pool="rbd"} 4`),
				regexp.MustCompile(`pool_write_total{cluster="ceph",pool="rbd"} 6`),
			},
			reUnmatch: []*regexp.Regexp{},
		},
		{
			input: `
{"pools": [
	{"name": "rbd", "id": 11, "stats": {"objects": 5, "rd": 4, "wr": 6}}
]}`,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`pool_used_bytes{cluster="ceph",pool="rbd"} 0`),
				regexp.MustCompile(`pool_objects_total{cluster="ceph",pool="rbd"} 5`),
				regexp.MustCompile(`pool_read_total{cluster="ceph",pool="rbd"} 4`),
				regexp.MustCompile(`pool_write_total{cluster="ceph",pool="rbd"} 6`),
			},
			reUnmatch: []*regexp.Regexp{},
		},
		{
			input: `
{"pools": [
	{"name": "rbd", "id": 11, "stats": {"stored": 20, "rd": 4, "wr": 6}}
]}`,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`pool_used_bytes{cluster="ceph",pool="rbd"} 20`),
				regexp.MustCompile(`pool_objects_total{cluster="ceph",pool="rbd"} 0`),
				regexp.MustCompile(`pool_read_total{cluster="ceph",pool="rbd"} 4`),
				regexp.MustCompile(`pool_write_total{cluster="ceph",pool="rbd"} 6`),
			},
			reUnmatch: []*regexp.Regexp{},
		},
		{
			input: `
{"pools": [
	{"name": "rbd", "id": 11, "stats": {"stored": 20, "objects": 5, "wr": 6}}
]}`,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`pool_used_bytes{cluster="ceph",pool="rbd"} 20`),
				regexp.MustCompile(`pool_objects_total{cluster="ceph",pool="rbd"} 5`),
				regexp.MustCompile(`pool_read_total{cluster="ceph",pool="rbd"} 0`),
				regexp.MustCompile(`pool_write_total{cluster="ceph",pool="rbd"} 6`),
			},
			reUnmatch: []*regexp.Regexp{},
		},
		{
			input: `
{"pools": [
	{"name": "rbd", "id": 11, "stats": {"stored": 20, "objects": 5, "rd": 4}}
]}`,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`pool_used_bytes{cluster="ceph",pool="rbd"} 20`),
				regexp.MustCompile(`pool_objects_total{cluster="ceph",pool="rbd"} 5`),
				regexp.MustCompile(`pool_read_total{cluster="ceph",pool="rbd"} 4`),
				regexp.MustCompile(`pool_write_total{cluster="ceph",pool="rbd"} 0`),
			},
			reUnmatch: []*regexp.Regexp{},
		},
		{
			input: `
{"pools": [
    {{{{"name": "rbd", "id": 11, "stats": {"stored": 20, "objects": 5, "rd": 4, "wr": 6}}
]}`,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{},
			reUnmatch: []*regexp.Regexp{
				regexp.MustCompile(`pool_used_bytes{cluster="ceph"}`),
				regexp.MustCompile(`pool_objects_total{cluster="ceph"}`),
				regexp.MustCompile(`pool_read_total{cluster="ceph"}`),
				regexp.MustCompile(`pool_write_total{cluster="ceph"}`),
			},
		},
		{
			input: `
{"pools": [
	{"name": "rbd", "id": 11, "stats": {"stored": 20, "objects": 5, "rd": 4, "wr": 6}},
	{"name": "rbd-new", "id": 12, "stats": {"stored": 50, "objects": 20, "rd": 10, "wr": 30}}
]}`,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`pool_used_bytes{cluster="ceph",pool="rbd"} 20`),
				regexp.MustCompile(`pool_objects_total{cluster="ceph",pool="rbd"} 5`),
				regexp.MustCompile(`pool_read_total{cluster="ceph",pool="rbd"} 4`),
				regexp.MustCompile(`pool_write_total{cluster="ceph",pool="rbd"} 6`),
				regexp.MustCompile(`pool_used_bytes{cluster="ceph",pool="rbd-new"} 50`),
				regexp.MustCompile(`pool_objects_total{cluster="ceph",pool="rbd-new"} 20`),
				regexp.MustCompile(`pool_read_total{cluster="ceph",pool="rbd-new"} 10`),
				regexp.MustCompile(`pool_write_total{cluster="ceph",pool="rbd-new"} 30`),
			},
			reUnmatch: []*regexp.Regexp{},
		},
		{
			input: `
{"pools": [
	{"name": "ssd", "id": 11, "stats": {"max_avail": 4618201748262, "objects": 5, "rd": 4, "wr": 6}}
]}`,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`pool_available_bytes{cluster="ceph",pool="ssd"} 4.618201748262e\+12`),
			},
		},
		{
			input: `
{"pools": [
	{"name": "ssd", "id": 11, "stats": {"percent_used": 1.3390908861765638e-06, "objects": 5, "rd": 4, "wr": 6}}
]}`,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`pool_percent_used{cluster="ceph",pool="ssd"} 1.3390908861765638e\-06`),
			},
		},
		{
			input: `
{"pools": [
	{"id": 32, "name": "cinder_sas", "stats": { "stored": 71525351713, "dirty": 17124, "kb_used": 69848977, "max_avail": 6038098673664, "objects": 17124, "quota_bytes": 0, "quota_objects": 0, "stored_raw": 214576054272, "rd": 348986643, "rd_bytes": 3288983853056, "wr": 45792703, "wr_bytes": 272268791808 }},
	{"id": 33, "name": "cinder_ssd", "stats": { "stored": 68865564849, "dirty": 16461, "kb_used": 67251529, "max_avail": 186205372416, "objects": 16461, "quota_bytes": 0, "quota_objects": 0, "stored_raw": 206596702208, "rd": 347, "rd_bytes": 12899328, "wr": 26721, "wr_bytes": 68882356224 }}
]}`,
			version: `{"version":"ceph version 16.2.11-22-wasd (1984a8c33225d70559cdf27dbab81e3ce153f6ac) pacific (stable)"}`,
			reMatch: []*regexp.Regexp{
				regexp.MustCompile(`ceph_pool_available_bytes{cluster="ceph",pool="cinder_sas"} 6.038098673664e\+12`),
				regexp.MustCompile(`ceph_pool_dirty_objects_total{cluster="ceph",pool="cinder_sas"} 17124`),
				regexp.MustCompile(`ceph_pool_objects_total{cluster="ceph",pool="cinder_sas"} 17124`),
				regexp.MustCompile(`ceph_pool_raw_used_bytes{cluster="ceph",pool="cinder_sas"} 2.14576054272e\+11`),
				regexp.MustCompile(`ceph_pool_read_bytes_total{cluster="ceph",pool="cinder_sas"} 3.288983853056e\+12`),
				regexp.MustCompile(`ceph_pool_read_total{cluster="ceph",pool="cinder_sas"} 3.48986643e\+08`),
				regexp.MustCompile(`ceph_pool_used_bytes{cluster="ceph",pool="cinder_sas"} 7.1525351713e\+10`),
				regexp.MustCompile(`ceph_pool_write_bytes_total{cluster="ceph",pool="cinder_sas"} 2.72268791808e\+11`),
				regexp.MustCompile(`ceph_pool_write_total{cluster="ceph",pool="cinder_sas"} 4.5792703e\+07`),
				regexp.MustCompile(`ceph_pool_available_bytes{cluster="ceph",pool="cinder_ssd"} 1.86205372416e\+11`),
				regexp.MustCompile(`ceph_pool_dirty_objects_total{cluster="ceph",pool="cinder_ssd"} 16461`),
				regexp.MustCompile(`ceph_pool_objects_total{cluster="ceph",pool="cinder_ssd"} 16461`),
				regexp.MustCompile(`ceph_pool_raw_used_bytes{cluster="ceph",pool="cinder_ssd"} 2.06596702208e\+11`),
				regexp.MustCompile(`ceph_pool_read_bytes_total{cluster="ceph",pool="cinder_ssd"} 1.2899328e\+07`),
				regexp.MustCompile(`ceph_pool_read_total{cluster="ceph",pool="cinder_ssd"} 347`),
				regexp.MustCompile(`ceph_pool_used_bytes{cluster="ceph",pool="cinder_ssd"} 6.8865564849e\+10`),
				regexp.MustCompile(`ceph_pool_write_bytes_total{cluster="ceph",pool="cinder_ssd"} 6.8882356224e\+10`),
				regexp.MustCompile(`ceph_pool_write_total{cluster="ceph",pool="cinder_ssd"} 26721`),
			},
		},
	} {
		func() {
			conn := setupVersionMocks(tt.version, "{}")

			conn.On("MonCommand", mock.Anything).Return(
				[]byte(tt.input), "", nil,
			)

			conn.On("GetPoolStats", mock.Anything).Return(
				nil, fmt.Errorf("not implemented"),
			)

			e := &Exporter{Conn: conn, Cluster: "ceph", Logger: logrus.New()}
			e.cc = map[string]versionedCollector{
				"poolUsage": NewPoolUsageCollector(e),
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
				require.True(t, re.Match(buf))
			}
			for _, re := range tt.reUnmatch {
				require.False(t, re.Match(buf))
			}
		}()
	}
}
