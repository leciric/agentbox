// The checks the daemon makes on a cluster's master URL and sizes
// (internal/state/clusters.go: clusterMasterURL, checkClusterSizes), made here
// first so a form says what is wrong beside the field rather than after a round
// trip. The daemon's are still the ones that count.

import type * as T from '../../shared/api';

// MasterURLProblem is why a master URL would be refused, one per rule of
// clusterMasterURL, or '' for one it takes (empty included: a cluster may be
// added before its address is known).
export type MasterURLProblem = '' | 'scheme' | 'host' | 'login' | 'query' | 'fragment';

export function masterURLProblem(raw: string): MasterURLProblem {
  const value = raw.trim();
  if (value === '') return '';
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    // URL refuses a relative address and one with no host the same way; Go's
    // parser takes both, then the scheme check refuses them.
    return /^https?:\/\//i.test(value) ? 'host' : 'scheme';
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return 'scheme';
  if (parsed.host === '') return 'host';
  if (parsed.username !== '' || parsed.password !== '') return 'login';
  // A bare "?" is a query to Go (ForceQuery) but an empty search to URL.
  if (parsed.search !== '' || /\?/.test(value.split('#')[0])) return 'query';
  if (parsed.hash !== '' || value.includes('#')) return 'fragment';
  return '';
}

// normalizeMasterURL is the address as the daemon stores it: trimmed, without
// the trailing slash people copy it with.
export function normalizeMasterURL(raw: string): string {
  return raw.trim().replace(/\/+$/, '');
}

// A Kubernetes quantity, as resource.ParseQuantity reads one: a number, then a
// binary suffix (Ki…Ei), a decimal one (n, u, m, k, M…E) or an exponent.
const quantity = /^\+?(\d+(\.\d*)?|\.\d+)([eE][+-]?\d+|[KMGTPE]i|[numkMGTPE])?$/;

// sizeProblem is why a size would be refused: 'invalid' for what isn't a
// quantity, 'zero' for one that gives a pod no room, '' for empty (the
// daemon's default) or a good one.
export function sizeProblem(raw: string): '' | 'invalid' | 'zero' {
  const value = raw.trim();
  if (value === '') return '';
  const match = quantity.exec(value);
  if (!match) return 'invalid';
  return Number(match[1]) > 0 ? '' : 'zero';
}

// maxAgentsProblem is the same for --max-agents: a whole number, 0 for as many
// as the nodes allow.
export function maxAgentsProblem(raw: string): '' | 'invalid' {
  return /^\d+$/.test(raw.trim()) || raw.trim() === '' ? '' : 'invalid';
}

export const tailnets = ['none', 'operator', 'sidecar'] as const;

// The sizes a cluster's edit form sets, by the field of ClusterConfig each is.
export const sizeFields = ['cpu', 'memory', 'agentCPU', 'agentMemory', 'workspaceSize', 'ephemeralStorage'] as const;
export type SizeField = (typeof sizeFields)[number];

// ClusterEdit is what the edit form holds, as text.
export type ClusterEdit = Record<SizeField, string> & { maxAgents: string; masterURL: string; tailnet: string };

export function clusterEdit(cluster: T.Cluster): ClusterEdit {
  return {
    cpu: cluster.cpu ?? '',
    memory: cluster.memory ?? '',
    agentCPU: cluster.agentCPU ?? '',
    agentMemory: cluster.agentMemory ?? '',
    workspaceSize: cluster.workspaceSize ?? '',
    ephemeralStorage: cluster.ephemeralStorage ?? '',
    maxAgents: String(cluster.maxAgents),
    masterURL: cluster.masterURL ?? '',
    tailnet: cluster.tailnet || 'none',
  };
}

// clusterConfig is the PUT body for an edit. PUT replaces every field, so what
// the form doesn't show (image, registry, storage class, labels, capabilities)
// is the cluster's own, sent back as it is.
export function clusterConfig(cluster: T.Cluster, edit: ClusterEdit): T.ClusterConfig {
  const size = (value: string) => value.trim() || undefined;
  return {
    storageClass: cluster.storageClass,
    image: cluster.image,
    registry: cluster.registry,
    labels: cluster.labels,
    capabilities: cluster.capabilities,
    masterURL: normalizeMasterURL(edit.masterURL) || undefined,
    tailnet: edit.tailnet,
    maxAgents: Number(edit.maxAgents.trim() || '0'),
    cpu: size(edit.cpu),
    memory: size(edit.memory),
    agentCPU: size(edit.agentCPU),
    agentMemory: size(edit.agentMemory),
    workspaceSize: size(edit.workspaceSize),
    ephemeralStorage: size(edit.ephemeralStorage),
  };
}

// editChanges is which fields an edit changes, compared as the daemon stores
// them; an empty list is an edit with nothing to save.
export function editChanges(cluster: T.Cluster, edit: ClusterEdit): (keyof ClusterEdit)[] {
  const before = clusterEdit(cluster);
  return (Object.keys(before) as (keyof ClusterEdit)[]).filter((key) =>
    key === 'masterURL'
      ? normalizeMasterURL(before.masterURL) !== normalizeMasterURL(edit.masterURL)
      : key === 'maxAgents'
        ? Number(before.maxAgents) !== Number(edit.maxAgents.trim() || '0')
        : before[key].trim() !== edit[key].trim(),
  );
}
