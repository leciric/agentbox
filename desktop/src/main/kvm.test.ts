// Run with `npm test` (node's own test runner, which strips the types).
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { canVirtualize, kvmProblem, type KVMProbe } from './kvm.ts';

const intel = 'processor\t: 0\nvendor_id\t: GenuineIntel\nflags\t\t: fpu vme de pse tsc msr vmx smx est\n';
const amd = 'processor\t: 0\nvendor_id\t: AuthenticAMD\nflags\t\t: fpu vme svm extapic\n';
// A VM without nested virtualization, or VT-x off in the firmware.
const plain = 'processor\t: 0\nvendor_id\t: GenuineIntel\nflags\t\t: fpu vme de pse tsc msr hypervisor\n';

function machine(canOpen: boolean, exists: boolean, cpuinfo = intel): KVMProbe {
  return { canOpen: () => canOpen, exists: () => exists, cpuinfo: () => cpuinfo };
}

test('a /dev/kvm this user opens is no problem', () => {
  assert.equal(kvmProblem(machine(true, true)), null);
});

test("a /dev/kvm this user can't open is one to grant (Debian, Ubuntu)", () => {
  assert.equal(kvmProblem(machine(false, true)), 'denied');
  // Whatever the CPU says: the device is there.
  assert.equal(kvmProblem(machine(false, true, plain)), 'denied');
});

test('no /dev/kvm on a CPU that can virtualize is a module to load', () => {
  assert.equal(kvmProblem(machine(false, false, intel)), 'module');
  assert.equal(kvmProblem(machine(false, false, amd)), 'module');
});

test("no /dev/kvm on a CPU that can't is the firmware's, or a VM's", () => {
  assert.equal(kvmProblem(machine(false, false, plain)), 'novirt');
  assert.equal(kvmProblem(machine(false, false, '')), 'novirt');
});

test('canVirtualize reads only the flags line, word by word', () => {
  assert.equal(canVirtualize(intel), true);
  assert.equal(canVirtualize(amd), true);
  assert.equal(canVirtualize(plain), false);
  // vmx as part of another word, or outside the flags, doesn't count.
  assert.equal(canVirtualize('model name\t: vmx svm\nflags\t\t: fpu vmxe svmx\n'), false);
});
