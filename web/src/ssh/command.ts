// SPDX-License-Identifier: AGPL-3.0-only

export function formatSSHCommand(instanceName: string, host: string, port: number): string {
  return `ssh ${instanceName}@${host} -p ${port}`;
}
