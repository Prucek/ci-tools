package multi_stage

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/GoogleCloudPlatform/secrets-store-csi-driver-provider-gcp/config"

	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	csiapi "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/openshift/ci-tools/pkg/api"
)

// GSMproject is the name of the GCP Secret Manager project where the secrets are stored.
var GSMproject = "openshift-ci-secrets"

// groupCredentialsByCollectionAndMountPath groups credentials by (collection, mount_path)
// to avoid duplicate mount paths.
func groupCredentialsByCollectionAndMountPath(credentials []api.CredentialReference) map[string][]api.CredentialReference {
	mountGroups := make(map[string][]api.CredentialReference)
	for _, credential := range credentials {
		key := fmt.Sprintf("%s:%s", credential.Collection, credential.MountPath)
		mountGroups[key] = append(mountGroups[key], credential)
	}
	return mountGroups
}

func buildGCPSecretsParameter(credentials []api.CredentialReference) (string, error) {
	var secrets []config.Secret
	for _, credential := range credentials {
		secrets = append(secrets, config.Secret{
			ResourceName: fmt.Sprintf("projects/%s/secrets/%s__%s/versions/latest", GSMproject, credential.Collection, credential.Name),
			FileName:     credential.Name, // we want to mount the secret as a file named without the collection prefix
		})
	}
	secretsYaml, err := yaml.Marshal(secrets)
	if err != nil {
		return "", fmt.Errorf("could not marshal secrets: %w", err)
	}
	return string(secretsYaml), nil
}

// getSPCName gets the unique SPC name for a collection, mount path, and credential contents
func getSPCName(namespace, collection, mountPath string, credentials []api.CredentialReference) string {
	var parts []string
	parts = append(parts, collection, mountPath)

	// Sort credential names for deterministic hashing
	var credNames []string
	for _, cred := range credentials {
		credNames = append(credNames, cred.Name)
	}
	sort.Strings(credNames)
	parts = append(parts, credNames...)

	hash := sha256.Sum256([]byte(strings.Join(parts, "-")))
	hashStr := fmt.Sprintf("%x", hash[:12])
	name := fmt.Sprintf("%s-%s-spc", namespace, hashStr)

	return strings.ToLower(name)
}

// getCSIVolumeName generates a deterministic, DNS-compliant name for a CSI volume
// based on the namespace, collection, and mountPath. The name is constructed as
// "<namespace>-<hash>", where the hash is computed from the collection and mountPath.
// If the resulting name exceeds 63 characters (the Kubernetes DNS label limit),
// only the hash is used as the name.
func getCSIVolumeName(ns, collection, mountPath string) string {
	// Hash both collection and mountPath together for consistent length
	hash := sha256.Sum256([]byte(strings.Join([]string{collection, mountPath}, "-")))
	hashStr := fmt.Sprintf("%x", hash[:8])
	name := fmt.Sprintf("%s-%s", ns, hashStr)

	// If namespace + hash is still too long, use just the hash
	if len(name) > 63 {
		hashStr := fmt.Sprintf("%x", hash[:16])
		name = hashStr
	}

	return strings.ToLower(name)
}

func getCensorMountPath(secretName string) string {
	return fmt.Sprintf("/censor/%s", secretName)
}

func buildSecretProviderClass(name, namespace, secrets string) *csiapi.SecretProviderClass {
	return &csiapi.SecretProviderClass{
		TypeMeta: meta.TypeMeta{
			Kind:       "SecretProviderClass",
			APIVersion: csiapi.GroupVersion.String(),
		},
		ObjectMeta: meta.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: csiapi.SecretProviderClassSpec{
			Provider: "gcp",
			Parameters: map[string]string{
				"auth":    "provider-adc",
				"secrets": secrets,
			},
		},
	}
}
