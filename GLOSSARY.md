# skybox

Provisions cloud machines where coding agents work, and links each one to T3 Code so threads can run there.

## Language

**Sandbox**:
One cloud machine skybox provisions for agent work, with its own disk, its agent account and a T3 Code server.
_Avoid_: Box, VM, instance (except for the cloud resource itself)

**Provider**:
The cloud a sandbox runs in, together with the code that creates, sleeps, wakes and deletes it there.
_Avoid_: Backend, driver, cloud (for the code)

**Agent account**:
The unprivileged Linux user on a sandbox that runs the T3 Code server and every agent.
_Avoid_: Service user, runner

**Environment**:
A sandbox as T3 Code sees it: a server reachable over T3 Connect, listed under Settings → Environments and in Run on once added.
_Avoid_: Connection, remote, target

**Sleep**:
Stopping a sandbox's compute while its disk stays, so it costs only storage until it wakes.
_Avoid_: Stop, pause, suspend, deallocate (except for the provider call)

**Wake**:
Starting a sleeping sandbox again with the disk it had.
_Avoid_: Start, resume

**Down**:
Deleting a sandbox and everything it holds.
_Avoid_: Destroy, remove, teardown
