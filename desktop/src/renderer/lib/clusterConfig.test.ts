// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as T from '../../shared/api';
import { clusterConfig, clusterEdit, editChanges, masterURLProblem, maxAgentsProblem, normalizeMasterURL, sizeProblem } from './clusterConfig.ts';

test('a master URL is checked as the daemon checks it', () => {
  // The cases of internal/state/clusters_test.go.
  assert.equal(masterURLProblem(''), '');
  assert.equal(masterURLProblem('https://monster.example.ts.net'), '');
  assert.equal(masterURLProblem('  http://host.k3d.internal:7782/ '), '');
  assert.equal(masterURLProblem('master.example.com'), 'scheme');
  assert.equal(masterURLProblem('ftp://master.example.com'), 'scheme');
  assert.equal(masterURLProblem('https://'), 'host');
  assert.equal(masterURLProblem('https://master.example.test?x=1'), 'query');
  assert.equal(masterURLProblem('https://master.example.test?'), 'query');
  assert.equal(masterURLProblem('https://master.example.test#x'), 'fragment');
  assert.equal(masterURLProblem('https://user:pass@master.example.test'), 'login');
});

test('a master URL is stored without the slash it was copied with', () => {
  assert.equal(normalizeMasterURL(' http://host.k3d.internal:7782/ '), 'http://host.k3d.internal:7782');
});

test('a size is a quantity greater than zero', () => {
  for (const good of ['', '2', '500m', '4Gi', '1.5', '10G', '1e3']) assert.equal(sizeProblem(good), '', good);
  for (const bad of ['4GB', 'two', '-1', '4 Gi']) assert.equal(sizeProblem(bad), 'invalid', bad);
  assert.equal(sizeProblem('0'), 'zero');
  assert.equal(sizeProblem('0Gi'), 'zero');
  assert.equal(maxAgentsProblem('4'), '');
  assert.equal(maxAgentsProblem('-1'), 'invalid');
  assert.equal(maxAgentsProblem('2.5'), 'invalid');
});

const cluster: T.Cluster = {
  name: 'k3d',
  namespace: 'agentbox',
  image: 'agentbox-agent:dev',
  registry: 'forgejo',
  storageClass: 'local-path',
  labels: { zone: 'a' },
  capabilities: ['gpu'],
  maxAgents: 4,
  memory: '8Gi',
  tailnet: 'none',
  createdAt: '2026-10-29T00:00:00Z',
};

test('an edit sends back what the form does not show', () => {
  const edit = { ...clusterEdit(cluster), masterURL: 'http://host.k3d.internal:7782/', cpu: ' 2 ', memory: '' };
  assert.deepEqual(clusterConfig(cluster, edit), {
    storageClass: 'local-path',
    image: 'agentbox-agent:dev',
    registry: 'forgejo',
    labels: { zone: 'a' },
    capabilities: ['gpu'],
    masterURL: 'http://host.k3d.internal:7782',
    tailnet: 'none',
    maxAgents: 4,
    cpu: '2',
    memory: undefined,
    agentCPU: undefined,
    agentMemory: undefined,
    workspaceSize: undefined,
    ephemeralStorage: undefined,
  });
});

test('an edit knows what it changes', () => {
  assert.deepEqual(editChanges(cluster, clusterEdit(cluster)), []);
  assert.deepEqual(editChanges({ ...cluster, masterURL: 'https://a.test' }, { ...clusterEdit(cluster), masterURL: 'https://a.test/' }), []);
  assert.deepEqual(editChanges(cluster, { ...clusterEdit(cluster), maxAgents: '6', tailnet: 'sidecar' }), ['maxAgents', 'tailnet']);
});
