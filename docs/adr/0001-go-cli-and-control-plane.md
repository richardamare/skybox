# The CLI and the control plane are Go binaries in one module

skybox ships as one Go module at the repo root with two binaries, `skybox` for the user and `skyboxctl` for the control plane. Go gives a single binary, a first-class Azure SDK, and existing T3 Code and SSH client code in the user's other Go tools to reuse. Rust and Effect/TypeScript were considered and rejected. Go sits outside the user's work stack (pnpm, .NET, Python), which is acceptable because skybox is a personal project.
