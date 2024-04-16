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
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
)

const (
	cephCmd                      = "/usr/bin/ceph"
	mdsBackgroundCollectInterval = 5 * time.Minute
)

const (
	MDSModeDisabled   = 0
	MDSModeForeground = 1
	MDSModeBackground = 2
)

type mdsStat struct {
	FSMap struct {
		Filesystems []struct {
			MDSMap struct {
				FSName string `json:"fs_name"`
				Info   map[string]struct {
					GID   uint   `json:"gid"`
					Name  string `json:"name"`
					Rank  int    `json:"rank"`
					State string `json:"state"`
				} `json:"info"`
			} `json:"mdsmap"`
		} `json:"filesystems"`
	} `json:"fsmap"`
}

// runMDSStat will run mds stat and get all info from the MDSs within the ceph cluster.
func runMDSStat(ctx context.Context, config, user string) ([]byte, error) {
	return exec.CommandContext(ctx, cephCmd, "-c", config, "-n", fmt.Sprintf("client.%s", user), "mds", "stat", "--format", "json").Output()
}

// runCephHealthDetail will run health detail and get info specific to MDSs within the ceph cluster.
func runCephHealthDetail(ctx context.Context, config, user string) ([]byte, error) {
	return exec.CommandContext(ctx, cephCmd, "-c", config, "-n", fmt.Sprintf("client.%s", user), "health", "detail", "--format", "json").Output()
}

// runMDSStatus will run status command on the MDS to get it's info.
func runMDSStatus(ctx context.Context, config, user, mds string) ([]byte, error) {
	return exec.CommandContext(ctx, cephCmd, "-c", config, "-n", fmt.Sprintf("client.%s", user), "tell", mds, "status").Output()
}

// runBlockedOpsCheck will run blocked ops on MDSs and get any ops that are blocked for that MDS.
func runBlockedOpsCheck(ctx context.Context, config, user, mds string) ([]byte, error) {
	return exec.CommandContext(ctx, cephCmd, "-c", config, "-n", fmt.Sprintf("client.%s", user), "tell", mds, "dump_blocked_ops").Output()
}

// MDSCollector collects metrics from the MDS daemons.
type MDSCollector struct {
	config     string
	user       string
	background bool
	logger     *logrus.Logger
	ch         chan prometheus.Metric

	// MDSState reports the state of MDS process running.
	MDSState *prometheus.Desc

	// MDSBlockedOPs reports the slow or blocked ops on an MDS.
	MDSBlockedOps *prometheus.Desc

	runMDSStatFn          func(context.Context, string, string) ([]byte, error)
	runCephHealthDetailFn func(context.Context, string, string) ([]byte, error)
	runMDSStatusFn        func(context.Context, string, string, string) ([]byte, error)
	runBlockedOpsCheckFn  func(context.Context, string, string, string) ([]byte, error)
}

// NewMDSCollector creates an instance of the MDSCollector and instantiates
// the individual metrics that we can collect from the MDS daemons.
func NewMDSCollector(exporter *Exporter, background bool) *MDSCollector {
	labels := make(prometheus.Labels)
	labels["cluster"] = exporter.Cluster

	mds := &MDSCollector{
		config:                exporter.Config,
		user:                  exporter.User,
		background:            background,
		logger:                exporter.Logger,
		ch:                    make(chan prometheus.Metric, 100),
		runMDSStatFn:          runMDSStat,
		runCephHealthDetailFn: runCephHealthDetail,
		runMDSStatusFn:        runMDSStatus,
		runBlockedOpsCheckFn:  runBlockedOpsCheck,

		MDSState: prometheus.NewDesc(
			fmt.Sprintf("%s_%s", cephNamespace, "mds_daemon_state"),
			"MDS Daemon State",
			[]string{"fs", "name", "rank", "state"},
			labels,
		),
		MDSBlockedOps: prometheus.NewDesc(
			fmt.Sprintf("%s_%s", cephNamespace, "mds_blocked_ops"),
			"MDS Blocked Ops",
			[]string{"fs", "name", "state", "optype", "fs_optype", "flag_point", "inode"},
			labels,
		),
	}

	return mds
}

func (m *MDSCollector) collectorList() []prometheus.Collector {
	return []prometheus.Collector{}
}

func (m *MDSCollector) descriptorList() []*prometheus.Desc {
	return []*prometheus.Desc{
		m.MDSState,
	}
}

func (m *MDSCollector) backgroundCollect() {
	defer close(m.ch)
	for {
		m.logger.WithField("background", m.background).Debug("collecting MDS stats")
		err := m.collect()
		if err != nil {
			m.logger.WithField("background", m.background).WithError(err).Error("error collecting MDS stats")
		}
		time.Sleep(mdsBackgroundCollectInterval)
	}
}

func (m *MDSCollector) collect() error {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	data, err := m.runMDSStatFn(ctx, m.config, m.user)
	if err != nil {
		return fmt.Errorf("failed getting mds stat: %w", err)
	}

	ms := &mdsStat{}

	err = json.Unmarshal(data, ms)
	if err != nil {
		return fmt.Errorf("failed unmarshalling mds stat json: %w", err)
	}

	for _, fs := range ms.FSMap.Filesystems {
		for _, info := range fs.MDSMap.Info {
			select {
			case m.ch <- prometheus.MustNewConstMetric(
				m.MDSState,
				prometheus.GaugeValue,
				float64(1),
				fs.MDSMap.FSName,
				info.Name,
				strconv.Itoa(info.Rank),
				info.State,
			):
			default:
			}
		}
	}

	m.collectMDSSlowOps()

	return nil
}

// Describe sends the descriptors of each MDSCollector related metrics we have defined
// to the provided prometheus channel.
func (m *MDSCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, metric := range m.collectorList() {
		metric.Describe(ch)
	}

	for _, metric := range m.descriptorList() {
		ch <- metric
	}
}

// Collect sends all the collected metrics to the provided prometheus channel.
// It requires the caller to handle synchronization.
func (m *MDSCollector) Collect(ch chan<- prometheus.Metric, version *Version) {
	if !m.background {
		m.logger.WithField("background", m.background).Debug("collecting MDS stats")
		err := m.collect()
		if err != nil {
			m.logger.WithField("background", m.background).WithError(err).Error("error collecting MDS stats")
		}
	}

	if m.background {
		go m.backgroundCollect()
	}

	for _, metric := range m.collectorList() {
		metric.Collect(ch)
	}

	for {
		select {
		case cc, ok := <-m.ch:
			if ok {
				ch <- cc
			}
		default:
			return
		}
	}
}

type healthDetailCheck struct {
	Status string `json:"status"`
	Checks map[string]struct {
		Severity string `json:"severity"`
		Summary  struct {
			Message string `json:"message"`
			Count   int    `json:"count"`
		} `json:"summary"`
		Detail []struct {
			Message string `json:"message"`
		} `json:"detail"`
		Muted bool `json:"muted"`
	} `json:"checks"`
}

type mdsStatus struct {
	ClusterFsid        string  `json:"cluster_fsid"`
	Whoami             int     `json:"whoami"`
	ID                 int64   `json:"id"`
	WantState          string  `json:"want_state"`
	State              string  `json:"state"`
	FsName             string  `json:"fs_name"`
	RankUptime         float64 `json:"rank_uptime"`
	MdsmapEpoch        int     `json:"mdsmap_epoch"`
	OsdmapEpoch        int     `json:"osdmap_epoch"`
	OsdmapEpochBarrier int     `json:"osdmap_epoch_barrier"`
	Uptime             float64 `json:"uptime"`
}

type mdsLabels struct {
	FSName    string
	MDSName   string
	State     string
	OpType    string
	FSOpType  string
	FlagPoint string
	Inode     string
}

func (ml mdsLabels) Hash() string {
	var b bytes.Buffer
	gob.NewEncoder(&b).Encode(ml)
	return b.String()
}

func (ml *mdsLabels) UnHash(hash string) error {
	return gob.NewDecoder(strings.NewReader(hash)).Decode(ml)

}

type mdsSlowOp struct {
	Ops []struct {
		// Custom fields for easy parsing by caller.
		MDSName, CephFSOpType string

		// CephFS fields.
		Description string  `json:"description"`
		InitiatedAt string  `json:"initiated_at"`
		Age         float64 `json:"age"`
		Duration    float64 `json:"duration"`
		TypeData    struct {
			FlagPoint  string `json:"flag_point"`
			Reqid      string `json:"reqid"`
			OpType     string `json:"op_type"`
			ClientInfo struct {
				Client string `json:"client"`
				Tid    int    `json:"tid"`
			} `json:"client_info"`
			Events []struct {
				Time  string `json:"time"`
				Event string `json:"event"`
			} `json:"events"`
		} `json:"type_data,omitempty"`
	} `json:"ops"`
	ComplaintTime int `json:"complaint_time"`
	NumBlockedOps int `json:"num_blocked_ops"`
}

func (m *MDSCollector) collectMDSSlowOps() {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	data, err := m.runCephHealthDetailFn(ctx, m.config, m.user)
	if err != nil {
		m.logger.WithError(err).Error("failed getting health detail")
		return
	}

	hc := &healthDetailCheck{}

	err = json.Unmarshal(data, hc)
	if err != nil {
		m.logger.WithError(err).Error("failed unmarshalling health detail")
		return
	}

	check, ok := hc.Checks["MDS_SLOW_REQUEST"]
	if !ok {
		// No slow requests! Yay!
		return
	}

	for _, cc := range check.Detail {
		mdsNameParts := strings.Split(cc.Message, "(")
		if len(mdsNameParts) != 2 {
			m.logger.WithError(
				errors.New("incorrect part count"),
			).WithFields(logrus.Fields{
				"message": cc.Message,
			}).Error("invalid mds slow request message found, check syntax")
			continue
		}

		mdsName := mdsNameParts[0]

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		data, err := m.runMDSStatusFn(ctx, m.config, m.user, mdsName)
		if err != nil {
			m.logger.WithField("mds", mdsName).WithError(err).Error("failed getting status from mds")
			return
		}

		mss := &mdsStatus{}

		err = json.Unmarshal(data, mss)
		if err != nil {
			m.logger.WithField("mds", mdsName).WithError(err).Error("failed unmarshalling mds status")
			return
		}

		ctx, cancel = context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		data, err = m.runBlockedOpsCheckFn(ctx, m.config, m.user, mdsName)
		if err != nil {
			m.logger.WithField("mds", mdsName).WithError(err).Error("failed getting blocked ops from mds")
			return
		}

		mso := &mdsSlowOp{}

		err = json.Unmarshal(data, mso)
		if err != nil {
			m.logger.WithField("mds", mdsName).WithError(err).Error("failed unmarshalling mds blocked ops")
			return
		}

		var metricMap sync.Map

		for _, op := range mso.Ops {
			var ml mdsLabels

			if op.TypeData.OpType == "client_request" {
				opd, err := extractOpFromDescription(op.Description)
				if err != nil {
					m.logger.WithField("mds", mdsName).WithError(err).Error("failed parsing blocked ops description")
					continue
				}

				ml.FSOpType = opd.fsOpType
				ml.Inode = opd.inode
			}

			ml.FSName = mss.FsName
			ml.MDSName = mdsName
			ml.State = mss.State
			ml.OpType = op.TypeData.OpType
			ml.FlagPoint = op.TypeData.FlagPoint

			cnt, _ := metricMap.LoadOrStore(ml.Hash(), new(int32))
			v := cnt.(*int32)
			atomic.AddInt32(v, 1)
		}

		metricMap.Range(func(key, value any) bool {
			var ml mdsLabels
			ml.UnHash(fmt.Sprint(key))
			v := value.(*int32)

			select {
			case m.ch <- prometheus.MustNewConstMetric(
				m.MDSBlockedOps,
				prometheus.CounterValue,
				float64(*v),
				ml.FSName,
				ml.MDSName,
				ml.State,
				ml.OpType,
				ml.FSOpType,
				ml.FlagPoint,
				ml.Inode,
			):
			default:
			}

			return true
		})
	}
}

type opDesc struct {
	fsOpType string
	inode    string
	clientID string
}

var (
	descRegex                   = regexp.MustCompile(`client_request\(client\.(?P<clientid>[0-9].+?):(?P<cid>[0-9].+?)\s(?P<fsoptype>\w+)\s.*#(?P<inode>0x[0-9a-fA-F]+|[0-9]+)[^a-zA-Z\d:].*`)
	errInvalidDescriptionFormat = "invalid op description, unable to parse %q"
)

// extractOpFromDescription is designed to extract the fs optype from a given slow/blocked
// ops description.
//
// For e.g. given the description as follows:
//
//	"client_request(client.20001974182:344151 rmdir #0x10000000030/72a26231-ac24-4f69-9350-8ebc5444c9ea 2024-02-13T22:11:00.196767+0000 caller_uid=0, caller_gid=0{})"
//
// we should be able to extract the following fs optype out of it:
//
//	"rmdir"
func extractOpFromDescription(desc string) (*opDesc, error) {
	matches := descRegex.FindStringSubmatch(desc)
	if len(matches) == 0 {
		return nil, fmt.Errorf(errInvalidDescriptionFormat, desc)
	}

	groups := getGroups(*descRegex, desc)

	clientID, ok := groups["clientid"]
	if !ok {
		return nil, fmt.Errorf(errInvalidDescriptionFormat, desc)
	}

	fsoptype, ok := groups["fsoptype"]
	if !ok {
		return nil, fmt.Errorf(errInvalidDescriptionFormat, desc)
	}

	inode, ok := groups["inode"]
	if !ok {
		return nil, fmt.Errorf(errInvalidDescriptionFormat, desc)
	}

	return &opDesc{
		fsOpType: fsoptype,
		inode:    inode,
		clientID: clientID,
	}, nil
}

func getGroups(regEx regexp.Regexp, in string) map[string]string {
	match := regEx.FindStringSubmatch(in)

	groupsMap := make(map[string]string)
	for i, name := range regEx.SubexpNames() {
		if i > 0 && i <= len(match) {
			groupsMap[name] = match[i]
		}
	}
	return groupsMap
}
