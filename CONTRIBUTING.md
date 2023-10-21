# Contribution

👉 **if you want to fix a typo or improve an error message, you can write a comment in this [issue](https://github.com/inoxlang/inox/issues/4)**.

## Guidelines 

Before working on the codebase make sure you read [FUTURE.md](./FUTURE.md) and the [CLA Readme](.legal/CLA/README.md).

The following changes will **NOT** be accepted:
- adding a large dependency
- adding a dependency whose features can be easily implemented in the Inox repository
- adding a dependency with a copyleft license
- adding code without at least a few tests
- modifying the **core** package, unless you have a very good reason to do so

## Tests

**The code you add must be tested.** 

Run all tests with:
```
go test -race -count=1 -p=1 ./...
```

If you have Chrome installed you can set the env var RUN_BROWSER_AUTOMATION_EXAMPLES to "true". 

If you have a S3 bucket with read & write access you can the set the env variables read in the following [file](internal/globals/s3_ns/fs_test.go).

If you have a Cloudflare Account you can the set the env variables read in the following [file](internal/project/secrets_test.go).


## Save Memory Profile Of a Test

```
go test -p=1 -count=1 ./internal/core -short -race -timeout=100s -run=TestXXXX -memprofile mem.out
```

## Vetting

```
go vet ./...
```

## List Packages & Their CGo files
```
go list -f '{{with .Module}}{{.Path}}{{end}} {{.CgoFiles}}' -deps ./... | grep -v '^\s*\[\]$'
```