//  Copyright (c) 2017-2018 Uber Technologies, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package distributed

import (
	"encoding/json"
	"fmt"
	"github.com/samuel/go-zookeeper/zk"
	"github.com/uber/aresdb/common"
	"github.com/uber/aresdb/metastore"
	"os"
	"strings"
	"time"
)

//membership manager does several things:
//	1. it creates session based ephemeral node in zookeeper, to indicate current node's activeness
//	2. it manages cluster/remote mode specific jobs
type MembershipManager interface {
	// Connect to an AresDB cluster, this can mean communicating to ares controller or zk.
	// It also starts all periodical jobs
	Connect() error
	// Disconnect from cluster and stops jobs properly (if necessary)
	Disconnect()
}

// NewMembershipManager creates a new MembershipManager
func NewMembershipManager(cfg common.AresServerConfig, metaStore metastore.MetaStore) MembershipManager {
	return &membershipManagerImpl{
		cfg:       cfg,
		metaStore: metaStore,
	}
}

type membershipManagerImpl struct {
	cfg            common.AresServerConfig
	metaStore      metastore.MetaStore
	zkc            *zk.Conn
	schemaFetchJob *SchemaFetchJob
}

func (mm *membershipManagerImpl) Connect() (err error) {
	// connect to zk
	if mm.zkc == nil {
		err = mm.initZKConnection()
		if err != nil {
			return
		}
	}

	// join cluster
	var instanceName, hostName, clusterName string
	var serverPort int

	instanceName = mm.cfg.Cluster.InstanceName
	if instanceName == "" {
		err = ErrInvalidInstanceName
		return
	}
	hostName, err = os.Hostname()
	if err != nil {
		return
	}
	serverPort = mm.cfg.Port

	instance := Instance{
		Name: instanceName,
		Host: hostName,
		Port: serverPort,
	}
	clusterName = mm.cfg.Cluster.ClusterName

	var instanceBytes []byte
	instanceBytes, err = json.Marshal(instance)
	if err != nil {
		return
	}

	_, err = mm.zkc.Create(
		fmt.Sprintf("/ares_controller/%s/instances/%s", clusterName, instanceName),
		instanceBytes, zk.FlagEphemeral, zk.WorldACL(zk.PermAll))
	if err != nil {
		return
	}

	// start jobs
	mm.schemaFetchJob = NewSchemaFetchJob(mm.metaStore, metastore.NewTableSchameValidator(), clusterName, mm.zkc)
	err = mm.schemaFetchJob.FetchApplySchema(true)
	if err != nil {
		return
	}
	go mm.schemaFetchJob.Run()
	return
}

func (mm *membershipManagerImpl) Disconnect() {
	mm.zkc.Close()
	mm.schemaFetchJob.Stop()
	return
}

func (mm *membershipManagerImpl) initZKConnection() (err error) {
	zksStr := mm.cfg.Clients.ZK.ZKs
	zks := strings.Split(zksStr, ",")
	mm.zkc, _, err = zk.Connect(zks, time.Duration(mm.cfg.Clients.ZK.TimeoutSeconds)*time.Second)
	return
}