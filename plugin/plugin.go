package plugin

import (
	"context"
	"fmt"

	"github.com/gophercloud/gophercloud"
	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins"
	"github.com/hashicorp/nomad-autoscaler/plugins/base"
	"github.com/hashicorp/nomad-autoscaler/plugins/target"
	"github.com/hashicorp/nomad-autoscaler/sdk"
	"github.com/hashicorp/nomad-autoscaler/sdk/helper/nomad"
	"github.com/hashicorp/nomad-autoscaler/sdk/helper/scaleutils"
)

const (
	// pluginName is the unique name of the this plugin amongst Target plugins.
	pluginName = "openstack-senlin"

	// configKeys represents the known configuration parameters required at
	// varying points throughout the plugins lifecycle.
	configKeyRegion      = "os_region"
	configKeyClusterName = "os_senlin_cluster_name"
)

var (
	PluginConfig = &plugins.InternalPluginConfig{
		Factory: func(l hclog.Logger) interface{} { return NewOSSenlinPlugin(l) },
	}

	pluginInfo = &base.PluginInfo{
		Name:       pluginName,
		PluginType: sdk.PluginTypeTarget,
	}
)

// Assert that TargetPlugin meets the target.Target interface.
var _ target.Target = (*TargetPlugin)(nil)

// TargetPlugin is the OpenStack Senlin implementation of the target.Target interface.
type TargetPlugin struct {
	config map[string]string
	logger hclog.Logger
	client *gophercloud.ServiceClient

	// clusterUtils provides general cluster scaling utilities for querying the
	// state of nodes pools and performing scaling tasks.
	clusterUtils *scaleutils.ClusterScaleUtils
}

// NewOSSenlinPlugin returns the OpenStack Senlin implementation of the target.Target
// interface.
func NewOSSenlinPlugin(log hclog.Logger) *TargetPlugin {
	return &TargetPlugin{
		logger: log,
	}
}

// SetConfig satisfies the SetConfig function on the base.Plugin interface.
func (t *TargetPlugin) SetConfig(config map[string]string) error {

	t.config = config

	if err := t.setupOSClients(config); err != nil {
		return err
	}

	clusterUtils, err := scaleutils.NewClusterScaleUtils(nomad.ConfigFromNamespacedMap(config), t.logger)
	if err != nil {
		return err
	}

	// Store and set the remote ID callback function.
	t.clusterUtils = clusterUtils
	t.clusterUtils.ClusterNodeIDLookupFunc = openstackNodeNameMap

	return nil
}

// PluginInfo satisfies the PluginInfo function on the base.Plugin interface.
func (t *TargetPlugin) PluginInfo() (*base.PluginInfo, error) {
	return pluginInfo, nil
}

// Scale satisfies the Scale function on the target.Target interface.
func (t *TargetPlugin) Scale(action sdk.ScalingAction, config map[string]string) error {

	// OpenStack Senlin can't support dry-run like Nomad, so just exit.
	if action.Count == sdk.StrategyActionMetaValueDryRunCount {
		return nil
	}

	// We cannot scale a cluster without knowing the cluster name.
	clusterName, ok := config[configKeyClusterName]
	if !ok {
		return fmt.Errorf("required config param %s not found", configKeyClusterName)
	}
	ctx := context.Background()

	// Describe the Senlin cluster. This serves to both validate the config value is
	// correct and ensure the OpenStack client is configured correctly. The response
	// can also be used when performing the scaling, meaning we only need to
	// call it once.
	curCluster, err := t.describeCluster(clusterName)
	if err != nil {
		return fmt.Errorf("failed to describe OpenStack Senlin Cluster: %v", err)
	}

	// The OpenStack Senlin target requires different details depending on which
	// direction we want to scale. Therefore calculate the direction and the
	// relevant number so we can correctly perform the OpenStack Senlin work.
	num, direction := t.calculateDirection(int64(curCluster.DesiredCapacity), action.Count)

	switch direction {
	case "in":
		err = t.scaleIn(ctx, curCluster, num, config)
	case "out":
		err = t.scaleOut(ctx, curCluster, num)
	default:
		t.logger.Info("scaling not required", "cluster_name", clusterName,
			"current_count", curCluster.DesiredCapacity, "strategy_count", action.Count)
		return nil
	}

	// If we received an error while scaling, format this with an outer message
	// so its nice for the operators and then return any error to the caller.
	if err != nil {
		err = fmt.Errorf("failed to perform scaling action: %v", err)
	}
	return err
}

// Status satisfies the Status function on the target.Target interface.
func (t *TargetPlugin) Status(config map[string]string) (*sdk.TargetStatus, error) {

	// We cannot get the status of a cluster if we don't know its name.
	clusterName, ok := config[configKeyClusterName]
	if !ok {
		return nil, fmt.Errorf("required config param %s not found", configKeyClusterName)
	}

	cluster, err := t.describeCluster(clusterName)
	if err != nil {
		return nil, fmt.Errorf("failed to describe OpenStack Senlin Cluster: %v", err)
	}

	resp := sdk.TargetStatus{
		Ready: cluster.Status == "ACTIVE",
		Count: int64(cluster.DesiredCapacity),
		Meta:  make(map[string]string),
	}

	return &resp, nil
}

func (t *TargetPlugin) calculateDirection(clusterDesired, strategyDesired int64) (int64, string) {

	if strategyDesired < clusterDesired {
		return clusterDesired - strategyDesired, "in"
	}
	if strategyDesired > clusterDesired {
		return strategyDesired - clusterDesired, "out"
	}
	return 0, ""
}
