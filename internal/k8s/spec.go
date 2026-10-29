package k8s

import (
	"fmt"
	"slices"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Where an agent's pod keeps things. The workspace and the agent's home are two
// directories of one claim, so the volume is claimed once and survives a pod
// being replaced on another node.
const (
	// WorkspaceDir is where the agent's worktree is: the branch it works on,
	// checked out.
	WorkspaceDir = "/workspace"
	// HomeDir is the agent's home: its tools' logs, its shell history, the
	// caches that make a second start quick.
	HomeDir = "/home/agent"
	// RunDir is agentd's own: the git credential it is handed, its sockets,
	// anything that must not outlive the pod.
	RunDir = "/run/agentbox"
	// TokenDir is where the pod reads the bearer token it opens its session to
	// the master with. It is not under RunDir: /var/run is /run, agentd writes
	// in its own directory, and a read-only secret can't share one with it.
	TokenDir = "/run/agentbox-secrets"
	// TokenFile is the token inside TokenDir.
	TokenFile = TokenDir + "/token"
	// AgentUser is the UID and GID the agent runs as, everywhere: in an Incus
	// machine, in a pod, and the owner of its worktree.
	AgentUser = 1000
)

// Names of the things AgentBox makes for one agent. They are functions so that
// everything that has to agree on them does: the pod, its claim, its token
// Secret, and the reconciler looking one up by label rather than by name.
const agentPrefix = "ab-"

// PodName is the pod of one agent.
func PodName(project, agent string) string { return agentPrefix + project + "-" + agent }

// ClaimName is the claim its workspace and home live on.
func ClaimName(project, agent string) string { return PodName(project, agent) + "-workspace" }

// PodTokenSecretName is the Secret its session token is in. One name per agent,
// not per start: a restarted container re-reads the same file, and the master
// replaces the value when it hands out a new token.
func PodTokenSecretName(project, agent string) string { return PodName(project, agent) + "-token" }

// DefaultWorkspaceSize is an agent's claim when its cluster doesn't say: enough
// for a repository, its node_modules, its build artefacts and a Docker image or
// two.
const DefaultWorkspaceSize = "20Gi"

// DefaultEphemeralStorage is the local disk an agent's pod may fill when its
// cluster doesn't say: /tmp, /run and its container's writable layer. A pod
// that crosses it is evicted rather than filling its node, which is the guard
// this machine's disk floor is on a host with an Incus machine.
const DefaultEphemeralStorage = "10Gi"

// ephemeralRequest is what a pod asks for of the ephemeral-storage the scheduler
// counts. The limit is the size that matters, and asking for all of it would
// keep a node from packing agents by the room they actually use.
const ephemeralRequest = "1Gi"

// What agentd reads on start, as the container's environment. They are here, with
// the pod that carries them, so the master and the agent can't disagree about a
// name.
const (
	// EnvRef is <project>/<agent>.
	EnvRef = "AGENTBOX_REF"
	// EnvBranch is the branch it works on.
	EnvBranch = "AGENTBOX_BRANCH"
	// EnvBaseCommit is the commit that branch starts from.
	EnvBaseCommit = "AGENTBOX_BASE_COMMIT"
	// EnvMasterURL is where it opens its session, as this machine is reached
	// from the cluster.
	EnvMasterURL = "AGENTBOX_MASTER_URL"
	// EnvTokenFile is TokenFile: where its session token is.
	EnvTokenFile = "AGENTBOX_TOKEN_FILE"
	// EnvWorkspace is where its worktree goes, WorkspaceDir unless the cluster
	// says otherwise.
	EnvWorkspace = "AGENTBOX_WORKSPACE"
)

// Spec is one agent's pod, as the daemon builds it.
type Spec struct {
	// Cluster, Project and Agent name it. Its labels carry all three, which is
	// how the reconciler finds the pods of a cluster, a project or an agent.
	Cluster string
	Project string
	Agent   string
	// Image is the full reference to run, registry prefix and tag or digest
	// included.
	Image string
	// PullSecret is the image pull secret, "" for a registry that needs no login.
	PullSecret string
	// FromRegistry says the image is in a registry rather than imported into the
	// cluster, which decides whether a node trusts what it already has.
	FromRegistry bool
	// WorkspaceSize is the claim's size, DefaultWorkspaceSize when empty.
	WorkspaceSize string
	// EphemeralStorage is the local disk the pod may fill outside its claim,
	// its /tmp included, DefaultEphemeralStorage when empty.
	EphemeralStorage string
	// StorageClass is the claim's class, "" for the cluster's default.
	StorageClass string
	// Claim is the name of the persistent volume the pod's workspace and home
	// live on, "" for ClaimName(Project, Agent). A restore makes a new claim
	// rather than writing over the one an agent had, so the name is part of
	// the agent's row and not always the one its name derives.
	Claim string
	// CPU and Memory are what the pod asks for, as Kubernetes quantities. Empty
	// asks for nothing in particular, which is only right in a namespace whose
	// limits say otherwise.
	CPU    string
	Memory string
	// Env is what agentd reads on start: the agent's ref, the master's address,
	// its branch and base commit. Sorted into the container, so the same spec
	// always makes the same pod.
	Env map[string]string
	// MasterURL is where agentd opens its session, as this machine is reached
	// from the cluster. It is also in Env; this is the field the pod's own
	// probes and the doctor read.
	MasterURL string
	// HostAliases is where names the pod can't resolve answer, keyed by name.
	// A cluster's pods resolve what its nodes do, and the project's git remote
	// can be a name only the daemon knows, so the daemon resolves it and hands
	// the pod the address.
	HostAliases map[string]string
	// NodeSelector is the node labels the pod must be placed on, the cluster's
	// labels. Empty places it on any node.
	NodeSelector map[string]string
}

// Labels is what every object AgentBox makes for an agent carries, and what the
// reconciler selects on. It is a function, not a constant, because the values
// are the agent's.
func (s Spec) Labels() map[string]string {
	return map[string]string{
		LabelManaged: "true",
		LabelCluster: s.Cluster,
		LabelProject: s.Project,
		LabelAgent:   s.Agent,
		LabelApp:     AppAgent,
	}
}

// Annotations name the agent the way the user does.
func (s Spec) Annotations() map[string]string {
	return map[string]string{AnnotationRef: s.Project + "/" + s.Agent}
}

// WorkspaceClaim is the claim the pod mounts: the one the row names, or the
// name the agent's identity derives when it names none.
func (s Spec) WorkspaceClaim() string {
	if s.Claim != "" {
		return s.Claim
	}
	return ClaimName(s.Project, s.Agent)
}

// hostAliases is the pod's /etc/hosts entries, sorted by name so that the same
// spec always makes the same pod.
func (s Spec) hostAliases() []corev1.HostAlias {
	if len(s.HostAliases) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.HostAliases))
	for name := range s.HostAliases {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]corev1.HostAlias, 0, len(names))
	for _, name := range names {
		out = append(out, corev1.HostAlias{IP: s.HostAliases[name], Hostnames: []string{name}})
	}
	return out
}

// Object labels and annotations the reconciler and the app read.
const (
	LabelCluster = "agentbox/cluster"
	LabelProject = "agentbox/project"
	LabelAgent   = "agentbox/agent"
	// LabelApp and AppAgent are what every agent's pod is called by: the
	// standard application label, which is what the namespace's NetworkPolicy
	// selects. It says an agent runs here, where the labels above say which
	// one.
	LabelApp = "app.kubernetes.io/name"
	AppAgent = "agentbox-agent"
	// AnnotationRef is <project>/<agent>, the ref every command uses.
	AnnotationRef = "agentbox/ref"
	// AnnotationPodUID is the UID of the pod an agent's row was adopted from:
	// a pod the cluster recreated is a different one, and agentd's session on
	// the old one is over.
	AnnotationPodUID = "agentbox/pod-uid"
)

// agentOwnership is the owner the pod's init container gives the agent's
// directories: the image's own user, as uid:gid.
func agentOwnership() string {
	return strconv.Itoa(AgentUser) + ":" + strconv.Itoa(AgentUser)
}

// BuildPod is the pod one agent runs in: its machine.
//
// It runs agentd as PID 1, which is also its SIGTERM handler: Kubernetes asks it
// to stop, it checkpoints, and it exits, inside the pod's grace period. There is
// no preStop hook asking a second process to ask it to stop.
func BuildPod(spec Spec) (*corev1.Pod, error) {
	if err := spec.check(); err != nil {
		return nil, err
	}
	name := PodName(spec.Project, spec.Agent)
	requests, limits, ephemeral, err := resourceLimits(spec)
	if err != nil {
		return nil, err
	}
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Labels:      spec.Labels(),
			Annotations: spec.Annotations(),
		},
		Spec: corev1.PodSpec{
			// The agent's own machine: nothing else runs in it, and it is
			// replaced rather than repaired.
			RestartPolicy:                 corev1.RestartPolicyAlways,
			TerminationGracePeriodSeconds: ptr(int64(60)),
			EnableServiceLinks:            ptr(false),
			NodeSelector:                  nodeSelector(spec.NodeSelector),
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot:        ptr(true),
				RunAsUser:           ptr(int64(AgentUser)),
				RunAsGroup:          ptr(int64(AgentUser)),
				FSGroup:             ptr(int64(AgentUser)),
				FSGroupChangePolicy: ptr(corev1.FSGroupChangeOnRootMismatch),
				SeccompProfile:      &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			ImagePullSecrets: pullSecrets(spec.PullSecret),
			// Names the pod would otherwise have to resolve for itself: its
			// git remote, on a cluster whose DNS is the node's and knows
			// nothing of the daemon's private names.
			HostAliases: spec.hostAliases(),
			// The claim's root belongs to root, and git refuses a repository
			// whose directory another user owns ("dubious ownership"), so the
			// two directories the agent lives in are handed to it before
			// agentd starts. Only the directories: everything below them is
			// written by the agent, as the agent, from then on.
			InitContainers: []corev1.Container{{
				Name:            "ownership",
				Image:           spec.Image,
				ImagePullPolicy: pullPolicy(spec.FromRegistry),
				Command:         []string{"chown", agentOwnership(), WorkspaceDir, HomeDir},
				SecurityContext: &corev1.SecurityContext{
					// The one container that is root, and only for chown.
					RunAsUser:                ptr(int64(0)),
					RunAsGroup:               ptr(int64(0)),
					RunAsNonRoot:             ptr(false),
					AllowPrivilegeEscalation: ptr(false),
					Capabilities: &corev1.Capabilities{
						Add:  []corev1.Capability{"CHOWN"},
						Drop: []corev1.Capability{"ALL"},
					},
				},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "home", MountPath: WorkspaceDir, SubPath: "workspace"},
					{Name: "home", MountPath: HomeDir, SubPath: "home"},
				},
			}},
			Volumes: []corev1.Volume{
				{
					Name: "home",
					VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: spec.WorkspaceClaim(),
					}},
				},
				{
					// The pod's ephemeral limit is the guard; this is the one
					// directory that grows without asking, so it gets the limit
					// too rather than filling the node before the kubelet acts.
					Name: "tmp",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
						SizeLimit: &ephemeral,
					}},
				},
				{
					// Tools that use shared memory, which /dev/shm's 64Mi
					// default is too small for.
					Name: "shm",
					VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium:    corev1.StorageMediumMemory,
						SizeLimit: quantityPtr("1Gi"),
					}},
				},
				{Name: "run", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
				{
					Name: "token",
					VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
						SecretName:  PodTokenSecretName(spec.Project, spec.Agent),
						DefaultMode: ptr(int32(0o400)),
					}},
				},
			},
			Containers: []corev1.Container{{
				Name:            "agent",
				Image:           spec.Image,
				ImagePullPolicy: pullPolicy(spec.FromRegistry),
				Command:         []string{"agentbox", "agentd"},
				Env:             env(spec),
				Resources:       corev1.ResourceRequirements{Requests: requests, Limits: limits},
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: ptr(false),
					// The root filesystem stays writable: the AI tools install
					// themselves into the image's own prefixes on a first chat,
					// which is what makes the image small.
					Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "home", MountPath: WorkspaceDir, SubPath: "workspace"},
					{Name: "home", MountPath: HomeDir, SubPath: "home"},
					{Name: "tmp", MountPath: "/tmp"},
					{Name: "shm", MountPath: "/dev/shm"},
					{Name: "run", MountPath: RunDir},
					{Name: "token", MountPath: TokenDir, ReadOnly: true},
				},
				// Nothing listens in a pod: agentd opens the connection out. The
				// probe says the process is alive, nothing more.
				StartupProbe: &corev1.Probe{
					ProbeHandler:        corev1.ProbeHandler{Exec: &corev1.ExecAction{Command: []string{"agentbox", "agentd", "health"}}},
					PeriodSeconds:       5,
					FailureThreshold:    60,
					InitialDelaySeconds: 2,
				},
			}},
		},
	}, nil
}

// check refuses a spec no pod could come from.
func (s Spec) check() error {
	switch {
	case s.Project == "" || s.Agent == "":
		return fmt.Errorf("an agent's pod needs a project and an agent, not %q/%q", s.Project, s.Agent)
	case s.Cluster == "":
		return fmt.Errorf("%s/%s has no cluster: a pod is made for one", s.Project, s.Agent)
	case s.Image == "":
		return fmt.Errorf("%s/%s has no image: set one on its cluster (agentbox cluster add --image)", s.Project, s.Agent)
	}
	if name := PodName(s.Project, s.Agent); len(name) > 63 {
		return fmt.Errorf("the pod name %q is %d characters, and a pod's name is at most 63", name, len(name))
	}
	return nil
}

// resourceLimits is what the pod asks for, and what it may use: CPU is capped at
// what it asks for, because a runaway build competing with another agent's chat
// is the thing the budget is for, and memory is capped a little above it, because
// a process that crosses a memory limit is killed and a small overshoot is
// normal. Ephemeral storage is always set: it is the pod's own disk guard, and an
// agent that fills its node takes every other agent on it with it.
func resourceLimits(spec Spec) (requests, limits corev1.ResourceList, ephemeral resource.Quantity, err error) {
	requests = corev1.ResourceList{}
	limits = corev1.ResourceList{}
	if spec.CPU != "" {
		cpu, err := resource.ParseQuantity(spec.CPU)
		if err != nil {
			return nil, nil, ephemeral, fmt.Errorf("%s/%s's CPU %q: %w", spec.Project, spec.Agent, spec.CPU, err)
		}
		requests[corev1.ResourceCPU] = cpu
		limits[corev1.ResourceCPU] = cpu
	}
	if spec.Memory != "" {
		memory, err := resource.ParseQuantity(spec.Memory)
		if err != nil {
			return nil, nil, ephemeral, fmt.Errorf("%s/%s's memory %q: %w", spec.Project, spec.Agent, spec.Memory, err)
		}
		requests[corev1.ResourceMemory] = memory
		headroom := *resource.NewQuantity(memory.Value()/4, resource.BinarySI)
		limit := memory.DeepCopy()
		limit.Add(headroom)
		limits[corev1.ResourceMemory] = limit
	}
	size := spec.EphemeralStorage
	if size == "" {
		size = DefaultEphemeralStorage
	}
	ephemeral, err = resource.ParseQuantity(size)
	if err != nil {
		return nil, nil, ephemeral, fmt.Errorf("%s/%s's ephemeral storage %q: %w", spec.Project, spec.Agent, size, err)
	}
	ask := resource.MustParse(ephemeralRequest)
	if ephemeral.Cmp(ask) < 0 {
		ask = ephemeral.DeepCopy()
	}
	requests[corev1.ResourceEphemeralStorage] = ask
	limits[corev1.ResourceEphemeralStorage] = ephemeral
	return requests, limits, ephemeral, nil
}

// BuildPVC is the claim an agent's workspace and home live on: one claim, two
// directories, kept across a pod being replaced.
func BuildPVC(spec Spec) (*corev1.PersistentVolumeClaim, error) {
	if err := spec.check(); err != nil {
		return nil, err
	}
	size := spec.WorkspaceSize
	if size == "" {
		size = DefaultWorkspaceSize
	}
	storage, err := resource.ParseQuantity(size)
	if err != nil {
		return nil, fmt.Errorf("%s/%s's workspace size %q: %w", spec.Project, spec.Agent, size, err)
	}
	claim := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.WorkspaceClaim(),
			Labels:      spec.Labels(),
			Annotations: spec.Annotations(),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: storage},
			},
			StorageClassName: storageClass(spec.StorageClass),
		},
	}
	return claim, nil
}

// BuildTokenSecret is the Secret one agent's pod reads its bearer token from. The
// value is the token itself: the master keeps only its hash, in agent_tokens.
func BuildTokenSecret(spec Spec, token string) (*corev1.Secret, error) {
	if err := spec.check(); err != nil {
		return nil, err
	}
	if len(token) < 32 {
		return nil, fmt.Errorf("%s/%s's token is %d bytes: it is 32 bytes or more", spec.Project, spec.Agent, len(token))
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{
			Name:        PodTokenSecretName(spec.Project, spec.Agent),
			Labels:      spec.Labels(),
			Annotations: spec.Annotations(),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"token": []byte(token)},
	}, nil
}

// env is the container's environment, sorted by name so that the same spec always
// makes the same pod.
func env(spec Spec) []corev1.EnvVar {
	names := make([]string, 0, len(spec.Env))
	for name := range spec.Env {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]corev1.EnvVar, 0, len(names))
	for _, name := range names {
		out = append(out, corev1.EnvVar{Name: name, Value: spec.Env[name]})
	}
	return out
}

// pullPolicy is what a node does with an image. One in a registry is asked for
// again, because a tag we pushed may have moved; one that was imported into the
// cluster is used as it is, because there is nowhere to ask.
func pullPolicy(fromRegistry bool) corev1.PullPolicy {
	if fromRegistry {
		return corev1.PullAlways
	}
	return corev1.PullIfNotPresent
}

func quantityPtr(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}

// nodeSelector copies the labels, nil for none, so the pod never shares the
// spec's map and an unconstrained pod carries no empty selector.
func nodeSelector(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}
