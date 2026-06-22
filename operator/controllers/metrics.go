package controllers

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// clusterPullSecretSynced exposes the sync state of each ClusterPullSecret as a
// gauge (1 = synced, 0 = out of sync). Scrape it from the controller's metrics
// endpoint to alert on broken or missing credentials, e.g.:
//
//	pullsecret_operator_clusterpullsecret_synced == 0
var clusterPullSecretSynced = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "pullsecret_operator_clusterpullsecret_synced",
		Help: "Whether the ClusterPullSecret is synced (1) or out of sync (0).",
	},
	[]string{"clusterpullsecret"},
)

// registryAuthOK exposes whether each registry accepted the ClusterPullSecret's
// credentials (1) or explicitly rejected them (0). Alert on:
//
//	pullsecret_operator_registry_auth_ok == 0
var registryAuthOK = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "pullsecret_operator_registry_auth_ok",
		Help: "Whether the registry accepted the ClusterPullSecret's credentials (1) or rejected them (0).",
	},
	[]string{"clusterpullsecret", "registry"},
)

func init() {
	metrics.Registry.MustRegister(clusterPullSecretSynced, registryAuthOK)
}

// setSyncedMetric records the sync state for a ClusterPullSecret.
func setSyncedMetric(name string, synced bool) {
	v := 0.0
	if synced {
		v = 1.0
	}
	clusterPullSecretSynced.WithLabelValues(name).Set(v)
}

// setRegistryAuthMetric records whether a registry accepted the credentials.
func setRegistryAuthMetric(name, registry string, ok bool) {
	v := 0.0
	if ok {
		v = 1.0
	}
	registryAuthOK.WithLabelValues(name, registry).Set(v)
}

// clearSyncedMetric removes the metric series for a deleted ClusterPullSecret so it
// does not linger as a stale gauge.
func clearSyncedMetric(name string) {
	clusterPullSecretSynced.DeleteLabelValues(name)
	registryAuthOK.DeletePartialMatch(prometheus.Labels{"clusterpullsecret": name})
}
