# Node CA Persistence Correctness Fix Design

Date: 2026-07-06

## Goal

Fix two release-blocking correctness defects in automatic `NODE_EXTRA_CA_CERTS` persistence:

1. Always persist an absolute CA certificate path so Node.js and PowerShell behavior does not depend on the client's working directory.
2. Preserve an existing non-MCC-managed `NODE_EXTRA_CA_CERTS` value instead of overwriting user or corporate CA configuration.

Native Windows end-to-end acceptance remains a release gate, but it is verification work rather than a code change in this design.

## Chosen Approach

Centralize policy in `Executor.tryPersistNodeCA` and keep platform-specific value lookup behind `EnvAdapter`.

- Resolve `e.caCertPath` with `filepath.Abs` before certificate checks, marker checks, environment lookup, persistence, and marker writes.
- Extend `EnvAdapter` with a read operation for the currently persisted Node CA value.
- Windows reads `HKCU\Environment\NODE_EXTRA_CA_CERTS` directly.
- macOS first checks the process environment and then queries `launchctl getenv` when needed.
- Linux and other POSIX systems check the process environment; profile scanning remains the persistent-file safeguard.
- If the existing value is empty or already equals the desired absolute path, continue persistence so missing profiles can still be repaired.
- If the existing value differs, preserve it and return `ErrUserCustomValue`, unless the current user's existing MCC marker proves that exact value was previously managed by MCC. This exception allows legitimate CA-path migration.
- Lookup errors fail closed before any `setx`, `launchctl setenv`, or profile mutation.

This approach avoids duplicating policy across Windows, macOS, and POSIX writers and retains the existing `ErrUserCustomValue` result and user guidance.

## Data Flow

```text
configured CA path
    -> filepath.Abs
    -> certificate exists
    -> matching current marker? yes: ready
    -> privileged run? yes: reject
    -> read persisted NODE_EXTRA_CA_CERTS
         -> empty/same: continue
         -> differs and equals current-user marker's prior path: MCC migration, continue
         -> differs and not MCC-managed: preserve, ErrUserCustomValue
         -> lookup error: fail closed
    -> platform persistence using absolute path
    -> write marker using absolute path and current user identity
```

## Components

### Executor policy

`internal/bootstrap/bootstrap.go` owns absolute-path normalization, custom-value policy, prior-marker migration authorization, and marker updates.

### Environment lookup

`EnvAdapter` exposes the persisted value without changing it. The production adapter delegates to build-tagged implementations where OS APIs differ. Tests use the mock adapter to deterministically cover policy without touching the real registry or launchd session.

### Marker compatibility

The existing marker remains valid. A helper reads its prior `CertPath` only when the marker is a safe regular file and its recorded user identity matches the current user. Fingerprint mismatch does not prevent using the old path solely as evidence of prior MCC management, because relocation may make the old certificate unavailable.

## Error Handling

- `filepath.Abs` failure: return a failed attempted step without mutation.
- persisted-value lookup failure: return a failed attempted step without mutation.
- non-MCC custom value: return `ErrUserCustomValue` without mutation or marker update.
- existing desired value: continue platform persistence to repair profile/session coverage.
- old MCC-managed value: allow replacement with the new absolute path.
- persistence partial/failure behavior remains unchanged.

## Testing

Use red-green TDD for each behavior:

1. A relative CA path is passed to the adapter and written to the marker as an absolute path.
2. A different non-MCC persisted value returns `ErrUserCustomValue`, does not call persistence, and does not write a marker.
3. A persisted value equal to the desired absolute path still allows persistence to repair profiles.
4. A persisted value equal to the current user's prior MCC marker path is migrated to the new absolute path.
5. A persisted-value lookup error fails closed without mutation.
6. Windows registry lookup cross-compiles and has native tests where a Windows runner is available.
7. macOS/other lookup behavior has focused unit coverage through injectable hooks.
8. Run focused tests, full bootstrap tests, the repository race suite, `go vet`, frontend verification, and all six release builds.

## Non-Goals

- Do not merge multiple CA bundles.
- Do not overwrite or edit a user-managed CA file.
- Do not change system CA installation behavior.
- Do not redesign `PersistRoot`.
- Do not claim native Windows acceptance from cross-compilation alone.
