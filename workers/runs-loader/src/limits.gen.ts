// SPDX-License-Identifier: Apache-2.0
// Code generated from internal/services/runsloader.go by go generate; DO NOT EDIT.
// The loader's hard bounds. The server's own configured limits sit inside
// these; the loader refuses anything outside them whatever the server asks.

export const LIMITS = {
  requestBytes: 262144,
  codeBytes: 65536,
  inputBytes: 16384,
  cpuMsMax: 30000,
  wallMsMin: 100,
  wallMsMax: 25000,
  subrequestsMax: 64,
  outputBytesMax: 65536, // each of stdout, stderr and the result
  errorBytes: 2048,
  egressRequestsMax: 64,
  egressBytesOutMax: 1048576,
  egressBytesInMax: 8388608,
  egressLogMax: 128,
  denyEntriesMax: 256,
  hostBytesMax: 253,
  responseBytesMax: 524288,
  clockSkewSec: 60, // both ways: requests here, answers on the server
  tailWaitMs: 1500,
} as const;
