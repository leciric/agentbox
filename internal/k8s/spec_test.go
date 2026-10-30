package k8s_test

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"agentbox/internal/k8s"
)

func agentSpec() k8s.Spec {
	return k8s.Spec{
		Cluster:      "k3d",
		Project:      "pawly",
		Agent:        "agent-01",
		Image:        "hub.example.com/h4ad/agentbox-agent:dev",
		FromRegistry: true,
		PullSecret:   "agentbox-forgejo-pull",
		CPU:          "2",
		Memory:       "8Gi",
		Env: map[string]string{
			"AGENTBOX_REF":    "pawly/agent-01",
			"AGENTBOX_BRANCH": "agentbox/agent-01",
		},
	}
}

// container finds the agent's container in a pod.
func container(t *testing.T, pod *corev1.Pod) corev1.Container {
	t.Helper()
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("the pod has %d containers, want the one agent", len(pod.Spec.Containers))
	}
	return pod.Spec.Containers[0]
}

// mount finds where a volume is mounted.
func mount(t *testing.T, c corev1.Container, name string) corev1.VolumeMount {
	t.Helper()
	for _, m := range c.VolumeMounts {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("the container doesn't mount %s: %+v", name, c.VolumeMounts)
	return corev1.VolumeMount{}
}

// mountAt finds where a path is mounted.
func mountAt(t *testing.T, c corev1.Container, path string) corev1.VolumeMount {
	t.Helper()
	for _, m := range c.VolumeMounts {
		if m.MountPath == path {
			return m
		}
	}
	t.Fatalf("the container doesn't mount %s: %+v", path, c.VolumeMounts)
	return corev1.VolumeMount{}
}

// volume finds one of the pod's volumes.
func volume(t *testing.T, pod *corev1.Pod, name string) corev1.Volume {
	t.Helper()
	for _, v := range pod.Spec.Volumes {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("the pod has no %s volume: %+v", name, pod.Spec.Volumes)
	return corev1.Volume{}
}

func TestPodIsOneAgentsMachine(t *testing.T) {
	pod, err := k8s.BuildPod(agentSpec())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pod.Name, "ab-pawly-agent-01"; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	for label, want := range map[string]string{
		k8s.LabelManaged: "true",
		k8s.LabelCluster: "k3d",
		k8s.LabelProject: "pawly",
		k8s.LabelAgent:   "agent-01",
	} {
		if got := pod.Labels[label]; got != want {
			t.Errorf("label %s = %q, want %q", label, got, want)
		}
	}
	if got, want := pod.Annotations[k8s.AnnotationRef], "pawly/agent-01"; got != want {
		t.Errorf("%s = %q, want %q", k8s.AnnotationRef, got, want)
	}
	// Run as the agent, not as root, with a seccomp profile the kernel has.
	sc := pod.Spec.SecurityContext
	if sc == nil || sc.RunAsNonRoot == nil || !*sc.RunAsNonRoot || *sc.RunAsUser != k8s.AgentUser || *sc.FSGroup != k8s.AgentUser {
		t.Errorf("the pod's security context = %+v, want user and group %d, not root", sc, k8s.AgentUser)
	}
	if sc.SeccompProfile == nil || sc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("the pod's seccomp profile = %+v, want RuntimeDefault", sc.SeccompProfile)
	}
	if *pod.Spec.TerminationGracePeriodSeconds != 60 {
		t.Errorf("the grace period = %d, want 60", *pod.Spec.TerminationGracePeriodSeconds)
	}
	// Its own token is the Secret it is given; the namespace's default
	// ServiceAccount token would only be one more credential to read.
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Errorf("the ServiceAccount token is mounted = %v, want it not to be", pod.Spec.AutomountServiceAccountToken)
	}
	if len(pod.Spec.ImagePullSecrets) != 1 || pod.Spec.ImagePullSecrets[0].Name != "agentbox-forgejo-pull" {
		t.Errorf("pull secrets = %+v, want the registry's", pod.Spec.ImagePullSecrets)
	}

	c := container(t, pod)
	if c.Image != "hub.example.com/h4ad/agentbox-agent:dev" {
		t.Errorf("image = %q", c.Image)
	}
	// A tag we pushed may have moved, so a node asks again.
	if c.ImagePullPolicy != corev1.PullAlways {
		t.Errorf("pull policy = %q, want Always from a registry", c.ImagePullPolicy)
	}
	if len(c.Command) != 2 || strings.Join(c.Command, " ") != "agentbox agentd" {
		t.Errorf("command = %v, want agentbox agentd", c.Command)
	}
	if c.SecurityContext == nil || c.SecurityContext.AllowPrivilegeEscalation == nil ||
		*c.SecurityContext.AllowPrivilegeEscalation || len(c.SecurityContext.Capabilities.Drop) != 1 {
		t.Errorf("the container's security context = %+v, want no escalation and no capabilities", c.SecurityContext)
	}

	// The worktree and the home are two directories of one claim, so the volume
	// is claimed once and both survive a pod being replaced.
	workspace := mountAt(t, c, k8s.WorkspaceDir)
	if workspace.Name != "home" || workspace.SubPath != "workspace" {
		t.Errorf("the workspace mount = %+v, want subPath workspace of the agent's claim", workspace)
	}
	home := mountAt(t, c, k8s.HomeDir)
	if home.Name != "home" || home.SubPath != "home" {
		t.Errorf("the home mount = %+v, want subPath home of the agent's claim", home)
	}
	claim := volume(t, pod, "home").PersistentVolumeClaim
	if claim == nil || claim.ClaimName != "ab-pawly-agent-01-workspace" {
		t.Errorf("the workspace volume = %+v, want the agent's claim", claim)
	}
	if mount(t, c, "run").MountPath != k8s.RunDir {
		t.Errorf("agentd's run directory = %+v, want %s", mount(t, c, "run"), k8s.RunDir)
	}
	if shm := volume(t, pod, "shm").EmptyDir; shm == nil || shm.Medium != corev1.StorageMediumMemory {
		t.Errorf("the shm volume = %+v, want memory-backed", shm)
	}

	// The token is read-only, mode 0400, and not under agentd's writable dir.
	token := volume(t, pod, "token").Secret
	if token == nil || token.SecretName != "ab-pawly-agent-01-token" {
		t.Fatalf("the token volume = %+v, want the agent's token Secret", token)
	}
	if token.DefaultMode == nil || *token.DefaultMode != 0o400 {
		t.Errorf("the token's mode = %v, want 0400", token.DefaultMode)
	}
	tokenMount := mount(t, c, "token")
	if tokenMount.MountPath != k8s.TokenDir || !tokenMount.ReadOnly {
		t.Errorf("the token mount = %+v, want a read-only %s", tokenMount, k8s.TokenDir)
	}
	if strings.HasPrefix(k8s.TokenDir, k8s.RunDir+"/") {
		t.Errorf("the token is under agentd's writable directory: %s", k8s.TokenDir)
	}

	// The claim mounts as root, and git refuses a repository whose directory a
	// different user owns, so one root init container hands the agent its two
	// directories before agentd starts.
	if len(pod.Spec.InitContainers) != 1 {
		t.Fatalf("the pod has %d init containers, want one that owns the claim", len(pod.Spec.InitContainers))
	}
	init := pod.Spec.InitContainers[0]
	if got, want := strings.Join(init.Command, " "), "chown 1000:1000 /workspace /home/agent"; got != want {
		t.Errorf("the init command = %q, want %q", got, want)
	}
	isc := init.SecurityContext
	if isc == nil || isc.RunAsUser == nil || *isc.RunAsUser != 0 || isc.RunAsNonRoot == nil || *isc.RunAsNonRoot {
		t.Errorf("the init container's user = %+v, want root and only for the chown", isc)
	}
	if isc.Capabilities == nil || len(isc.Capabilities.Add) != 1 || isc.Capabilities.Add[0] != "CHOWN" ||
		len(isc.Capabilities.Drop) != 1 || isc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("the init container's capabilities = %+v, want CHOWN alone", isc.Capabilities)
	}
	if got := mountAt(t, init, k8s.WorkspaceDir); got.Name != "home" || got.SubPath != "workspace" {
		t.Errorf("the init container's workspace mount = %+v, want the claim's workspace", got)
	}
	if got := mountAt(t, init, k8s.HomeDir); got.Name != "home" || got.SubPath != "home" {
		t.Errorf("the init container's home mount = %+v, want the claim's home", got)
	}

	// What it asks for, and what it may use: CPU capped at the request, memory a
	// little above it.
	requests, limits := c.Resources.Requests, c.Resources.Limits
	if got := requests[corev1.ResourceCPU]; got.String() != "2" {
		t.Errorf("cpu request = %s, want 2", got.String())
	}
	if got := limits[corev1.ResourceCPU]; got.String() != "2" {
		t.Errorf("cpu limit = %s, want the request, 2", got.String())
	}
	if got := requests[corev1.ResourceMemory]; got.String() != "8Gi" {
		t.Errorf("memory request = %s, want 8Gi", got.String())
	}
	if got := limits[corev1.ResourceMemory]; got.String() != "10Gi" {
		t.Errorf("memory limit = %s, want a quarter more than the request, 10Gi", got.String())
	}
	// The pod's own disk is always bounded, even when the cluster says nothing:
	// one agent filling its node is every agent's problem.
	if got := limits[corev1.ResourceEphemeralStorage]; got.String() != k8s.DefaultEphemeralStorage {
		t.Errorf("ephemeral limit = %s, want the default %s", got.String(), k8s.DefaultEphemeralStorage)
	}
	if got := requests[corev1.ResourceEphemeralStorage]; got.String() != "1Gi" {
		t.Errorf("ephemeral request = %s, want 1Gi", got.String())
	}
	if tmp := volume(t, pod, "tmp").EmptyDir; tmp == nil || tmp.SizeLimit == nil || tmp.SizeLimit.String() != k8s.DefaultEphemeralStorage {
		t.Errorf("the /tmp limit = %+v, want the pod's ephemeral storage", tmp)
	}

	// Environment is sorted, so the same spec always makes the same pod.
	if len(c.Env) != 2 || c.Env[0].Name != "AGENTBOX_BRANCH" || c.Env[1].Name != "AGENTBOX_REF" {
		t.Errorf("env = %+v, want the two names in order", c.Env)
	}
}

func TestPodUsesAnImportedImageAsItIs(t *testing.T) {
	spec := agentSpec()
	spec.FromRegistry = false
	spec.PullSecret = ""
	spec.CPU, spec.Memory = "", ""
	pod, err := k8s.BuildPod(spec)
	if err != nil {
		t.Fatal(err)
	}
	c := container(t, pod)
	if c.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Errorf("pull policy = %q, want IfNotPresent for an imported image", c.ImagePullPolicy)
	}
	if len(pod.Spec.ImagePullSecrets) != 0 {
		t.Errorf("pull secrets = %+v, want none", pod.Spec.ImagePullSecrets)
	}
	if _, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
		t.Errorf("cpu = %+v, want none asked for", c.Resources.Requests)
	}
	if _, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
		t.Errorf("memory = %+v, want none asked for", c.Resources.Requests)
	}
}

// TestPodIsHandedTheNamesItCannotResolve: a cluster's pods resolve what its
// nodes resolve, so a git remote's name is resolved by the daemon and written
// into the pod's /etc/hosts, sorted so a spec always builds the same pod.
func TestPodIsHandedTheNamesItCannotResolve(t *testing.T) {
	spec := agentSpec()
	spec.HostAliases = map[string]string{
		"hub.example.ts.net": "203.0.113.7",
		"a.example":          "10.0.0.2",
	}
	pod, err := k8s.BuildPod(spec)
	if err != nil {
		t.Fatal(err)
	}
	want := []corev1.HostAlias{
		{IP: "10.0.0.2", Hostnames: []string{"a.example"}},
		{IP: "203.0.113.7", Hostnames: []string{"hub.example.ts.net"}},
	}
	if len(pod.Spec.HostAliases) != len(want) {
		t.Fatalf("host aliases = %+v, want %+v", pod.Spec.HostAliases, want)
	}
	for i, entry := range pod.Spec.HostAliases {
		if entry.IP != want[i].IP || strings.Join(entry.Hostnames, ",") != strings.Join(want[i].Hostnames, ",") {
			t.Errorf("host alias %d = %+v, want %+v", i, entry, want[i])
		}
	}
	// A pod whose remote is an address, or one that resolves nowhere, carries
	// no entries at all rather than an empty list.
	spec.HostAliases = nil
	plain, err := k8s.BuildPod(spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Spec.HostAliases) != 0 {
		t.Errorf("host aliases = %+v, want none", plain.Spec.HostAliases)
	}
}

func TestPodTakesTheClustersEphemeralStorage(t *testing.T) {
	spec := agentSpec()
	spec.EphemeralStorage = "3Gi"
	pod, err := k8s.BuildPod(spec)
	if err != nil {
		t.Fatal(err)
	}
	c := container(t, pod)
	if got := c.Resources.Limits[corev1.ResourceEphemeralStorage]; got.String() != "3Gi" {
		t.Errorf("ephemeral limit = %s, want 3Gi", got.String())
	}
	if tmp := volume(t, pod, "tmp").EmptyDir; tmp == nil || tmp.SizeLimit == nil || tmp.SizeLimit.String() != "3Gi" {
		t.Errorf("the /tmp limit = %+v, want 3Gi", tmp)
	}
	// A pod smaller than the request every other pod asks for can't ask for
	// more than it may use.
	spec.EphemeralStorage = "512Mi"
	pod, err = k8s.BuildPod(spec)
	if err != nil {
		t.Fatal(err)
	}
	if got := container(t, pod).Resources.Requests[corev1.ResourceEphemeralStorage]; got.String() != "512Mi" {
		t.Errorf("ephemeral request = %s, want 512Mi, no more than the limit", got.String())
	}
}

func TestPodRefusesWhatItCannotRun(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*k8s.Spec)
		wantErr string
	}{
		{name: "no agent", change: func(s *k8s.Spec) { s.Agent = "" }, wantErr: "needs a project and an agent"},
		{name: "no cluster", change: func(s *k8s.Spec) { s.Cluster = "" }, wantErr: "has no cluster"},
		{name: "no image", change: func(s *k8s.Spec) { s.Image = "" }, wantErr: "has no image"},
		{name: "a name too long", change: func(s *k8s.Spec) { s.Agent = strings.Repeat("a", 60) }, wantErr: "at most 63"},
		{name: "cpu that isn't a quantity", change: func(s *k8s.Spec) { s.CPU = "two" }, wantErr: "cpu"},
		{name: "memory that isn't a quantity", change: func(s *k8s.Spec) { s.Memory = "lots" }, wantErr: "memory"},
		{name: "ephemeral storage that isn't a quantity", change: func(s *k8s.Spec) { s.EphemeralStorage = "plenty" }, wantErr: "ephemeral storage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := agentSpec()
			tc.change(&spec)
			_, err := k8s.BuildPod(spec)
			if err == nil {
				t.Fatal("BuildPod accepted it")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantErr)) {
				t.Errorf("BuildPod() = %v, want it to say %q", err, tc.wantErr)
			}
		})
	}
}

func TestClaimHoldsTheWorkspaceAndHome(t *testing.T) {
	claim, err := k8s.BuildPVC(agentSpec())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := claim.Name, "ab-pawly-agent-01-workspace"; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	if len(claim.Spec.AccessModes) != 1 || claim.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("access modes = %v, want ReadWriteOnce", claim.Spec.AccessModes)
	}
	if got := claim.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != k8s.DefaultWorkspaceSize {
		t.Errorf("size = %s, want %s", got.String(), k8s.DefaultWorkspaceSize)
	}
	if claim.Spec.StorageClassName != nil {
		t.Errorf("storage class = %q, want the cluster's default", *claim.Spec.StorageClassName)
	}
	if claim.Labels[k8s.LabelAgent] != "agent-01" || claim.Annotations[k8s.AnnotationRef] != "pawly/agent-01" {
		t.Errorf("the claim isn't labelled for the agent: %v %v", claim.Labels, claim.Annotations)
	}

	// A cluster with its own class and size says so.
	spec := agentSpec()
	spec.StorageClass = "longhorn"
	spec.WorkspaceSize = "40Gi"
	claim, err = k8s.BuildPVC(spec)
	if err != nil {
		t.Fatal(err)
	}
	if claim.Spec.StorageClassName == nil || *claim.Spec.StorageClassName != "longhorn" {
		t.Errorf("storage class = %v, want longhorn", claim.Spec.StorageClassName)
	}
	if got := claim.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "40Gi" {
		t.Errorf("size = %s, want 40Gi", got.String())
	}
}

func TestTokenSecretIsTheAgentsSession(t *testing.T) {
	secret, err := k8s.BuildTokenSecret(agentSpec(), strings.Repeat("t", 32))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := secret.Name, "ab-pawly-agent-01-token"; got != want {
		t.Errorf("name = %q, want %q", got, want)
	}
	if got := string(secret.Data["token"]); got != strings.Repeat("t", 32) {
		t.Errorf("token = %q, want the one it was given", got)
	}
	if _, err := k8s.BuildTokenSecret(agentSpec(), "short"); err == nil {
		t.Error("a token too short to be one was accepted")
	}
}

func TestPodIsPlacedOnTheClustersLabels(t *testing.T) {
	spec := agentSpec()
	pod, err := k8s.BuildPod(spec)
	if err != nil {
		t.Fatal(err)
	}
	if pod.Spec.NodeSelector != nil {
		t.Errorf("a cluster with no labels got nodeSelector %v, want none", pod.Spec.NodeSelector)
	}
	spec.NodeSelector = map[string]string{"arch": "amd64", "gpu": "true"}
	pod, err = k8s.BuildPod(spec)
	if err != nil {
		t.Fatal(err)
	}
	got := pod.Spec.NodeSelector
	if len(got) != 2 || got["arch"] != "amd64" || got["gpu"] != "true" {
		t.Errorf("nodeSelector = %v, want arch=amd64 and gpu=true", got)
	}
	got["arch"] = "arm64"
	if spec.NodeSelector["arch"] != "amd64" {
		t.Error("the pod shares the spec's label map")
	}
}
