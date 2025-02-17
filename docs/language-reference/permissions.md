# Permissions

Permissions represent a type of action a module is allowed (or forbidden) to do.
Most IO operations (filesystem access, HTTP requests) and resource intensive
operations (lthread creation) necessitate a permission.

The [context](./context.md) of each module instance contains the granted and forbidden permissions.
The permissions granted to a module instance are defined in the [permissions section](./modules.md#permissions) of its manifest.

```mermaid
graph TD


subgraph Main
    ReadFS1(read %/...)
    WriteFS1(write %/...)
    ReadDB1(read ldb://main)
    WriteDB1(write ldb://main)
    HTTP1[make http requests]
end

subgraph CreateUser["/routes/POST-users.ix"]
    ReadDB2(read ldb://main)
    WriteDB2(write ldb://main)
    HTTP2[make http requests]
end

subgraph Lib["/lib.ix"]
    HTTP3[make http requests]
end

Main -.->|child| CreateUser
CreateUser -.->|child| Lib
```

The permission set of a descendant module is always a **subset** of its parent's permissions.

## Permission Dropping

Sometimes programs have an **initialization** phase, for example a program reads
a file or performs an HTTP request to fetch its configuration. After this phase
it no longer needs some permissions so it can drop them.

```
drop-perms {
  read: %https://**
}
```