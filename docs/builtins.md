
[Install Inox](../README.md#installation) | [Language Reference](./language-reference/language.md) | [Shell Basics](./shell-basics.md) | [Scripting Basics](./scripting-basics.md)

> This file is generated by an [Inox script](../scripts/gen-builtin-doc.ix).

-----

# Builtins

 - [Errors](#errors)
 - [Bytes and Runes](#bytes-and-runes)
 - [Browser Automation](#browser-automation)
 - [Concurrency And Execution](#concurrency-and-execution)
 - [Data Containers](#data-containers)
 - [Context data](#context-data)
 - [Conversion](#conversion)
 - [Cryptography](#cryptography)
 - [DNS](#dns)
 - [Encodings](#encodings)
 - [Filesystem](#filesystem)
 - [Functional Programming](#functional-programming)
 - [HTML](#html)
 - [HTTP](#http)
 - [ID Parsing](#id-parsing)
 - [Integer Utils](#integer-utils)
 - [Structured Logging](#structured-logging)
 - [Minimum & Maximum](#minimum-&-maximum)
 - [Printing](#printing)
 - [rand](#rand)
 - [Resource Manipulation](#resource-manipulation)
 - [Email Address](#email-address)
 - [TCP](#tcp)
 - [Time](#time)
## Errors

### Error

The `Error` function creates an error from the provided string and an optional immutable data argument.

**examples**

```inox
Error("failed to create user")
```
```inox
Error("failed to create user", #{user_id: 100})
```

## Bytes and Runes

### mkbytes

The `mkbytes` function allocates a byte-slice of the provided size.

**examples**

```inox
mkbytes(1kB)
```
### Bytes

The `Bytes` function reads a readable (string, byte-slice, ...) and returns a byte-slice.

**examples**

```inox
bytes = Bytes("abc")
```
### Runes

The `Runes` function reads a readable (string, byte-slice, ...) and returns a rune-slice. A rune is a Unicode code point (character). See https://go.dev/blog/strings for more details.

**examples**

```inox
runes = Runes("abc")
```
### is_space

The `is_space` function returns whether a given rune is a space character (Unicode's White Space property).

**examples**

```inox
is_space(' ') # true
```
### Reader

The `Reader` function creates a reader from a readable (string, byte-slice, ...).

**examples**

```inox
reader = Reader("abc")
bytes = reader.read_all!()

# abc
print(tostr(bytes))
```
### RingBuffer

The `RingBuffer` function creates a ring-buffer with a given capacity.

**examples**

```inox
buffer = RingBuffer(100B)
buffer.write!("abc")

# allocate a byte-slice to read from the buffer.
buf = mkbytes(100B)

# The read method writes to buf and returns the written slice of buf.
# Make sure to not modify the returned slice since doing so would mutate buf.
slice = buffer.read!(buf)

# abc
s = tostr(slice)
```

## Browser Automation

### chrome

chrome namespace.
### chrome.Handle

The `Handle` function creates a new Chrome handle that provides methods to interact with a web browser instance.
You should call its .close() method when you are finished using it. 

The project server downloads a Chromium browser
if none is present. The list of checked paths can be found here: https://github.com/inoxlang/inox/blob/main/internal/globals/chrome_ns/download.go#L114C1-L114C43.
If you are not using the project server you have to install Chrome or Chromium.

The browser instance always forwards the HTTP requests to a local proxy server that runs inside an Inox process.
Therefore make sure to add the necessary HTTP permissions in the manifest.

**examples**

```inox
chrome.Handle!()
```
### chrome.Handle/nav

The nav method makes the browser navigate to a page. All HTTP requests made by the browser are checked against the permission system, so make sure to add the necessary permissions.

**examples**

```inox
handle.nav https://go.dev/
```
### chrome.Handle/wait_visible

The wait_visible method waits until the DOM element matching the selector is visible.

**examples**

```inox
handle.wait_visible "div.title"
```
### chrome.Handle/click

The click method makes the browser click on the first DOM element matching the selector.

**examples**

```inox
handle.click "button.menu-item"
```
### chrome.Handle/screenshot

The screenshot method takes a screenshot of the first DOM element matching the selector.

**examples**

```inox
png_bytes = handle.screenshot!("#content")
```
### chrome.Handle/screenshot_page

The screenshot_page method takes a screenshot of the entire browser viewport.

**examples**

```inox
png_bytes = handle.screenshot_page!()
```
### chrome.Handle/html_node

The screenshot method gets the HTML of the first DOM element matching the selector, the result is %html.node not a string.

**examples**

```inox
png_bytes = handle.screenshot_page!()
```
### chrome.Handle/close

this method should be called when you are finished using the Chrome handle.

## Concurrency And Execution

### LThreadGroup

The `LThreadGroup` function creates a lightweight thread group. The group supports cancelling all lthreads added to it by calling its `cancel_all` method. Added lthreads can be waited for by calling the `wait_results` method of the group.

**examples**

```inox
LThreadGroup()
```
### ex

The `ex` function executes a command by name or by path in the OS filesystem. Executing commands requires the appropriate Inox permissions. The timeout duration for the execution can be configured by prefixing the command name (or path) with a duration range (e.g. ..5s),  it defaults to 500ms.

**examples**

```inox
ex echo "hello"
```
```inox
ex!(#echo, "hello")
```
```inox
ex!(/bin/echo "hello")
```
```inox
ex git --log
```
```inox
ex ..5s rm ./
```
### run

The `run` function executes an Inox script.

**examples**

```inox
run ./script.ix
```
### cancel_exec

The `cancel_exec` cancels the execution of the module.

**examples**

```inox
cancel_exec()
```

## Data Containers

### Graph

The `Graph` function creates a directed Graph.
### Tree

The `Tree` function creates a tree from a treedata value.

**examples**

```inox
Tree(treedata "root")
```
### Queue

The `Queue` function creates a queue from an iterable.

**examples**

```inox
Queue([])
```
```inox
Queue([1])
```
### Set

The `Set` function creates a set from an iterable, by default only representable (serializable) values are allowed. A configuration is accepted as a second argument.

**examples**

```inox
Set([])
```
```inox
Set([1])
```
```inox
Set([], {element: %int})
```
```inox
Set([{name: "A"}, {name: "B"}], {uniqueness: .name})
```
### Map

The `Map` function creates a map from a flat list of entries.

**examples**

```inox
Map(["key1", 10, "key2", 20])
```
### Ranking

The `Ranking` function creates a ranking from a flat list of entries. An entry is composed of a value and a floating-point score.  The value with the highest score has the first rank (0), values with the same score have the same rank.

**examples**

```inox
Ranking(["best player", 10.0, "other player", 5.0])
```
```inox
Ranking(["best player", 10.0, "other player", 10.0])
```

## Context data

### add_ctx_data

The `add_ctx_data` function creates a new user data entry in the context. The passed value should fall in one of the following categories: sharable, immutable, clonable. Adding an entry with an  existing entry's name is not allowed.

**examples**

```inox
user = {name: "foo"}

# `user` will be shared.
add_ctx_data(/user, user)
```
```inox
list = [1, 2, 3]

# `list` will be cloned.
add_ctx_data(/list, list)
```
### ctx_data

The `ctx_data` function retrieves the value of a user data entry. `nil` is returned if the entry does not exist. A pattern checking the value is accepted as a second argument.

**examples**

```inox
user = ctx_data(/user)
```
```inox
# ctx_data panics if the value does not match %user.
user = ctx_data(/user, %user)
```

## Conversion

### tostr

The `tostr` function converts its argument to a string-like value. Only the following types are supported: bool, int, byte-slice, rune-slice, path, host, url and all string-likes.
### tostring

The `tostring` function converts its argument to a string. `tostr` should always be used over `tostring`, unless you need a `string`, not a string-like value. Only the following types are supported: bool, int, byte-slice,  rune-slice, path, host, url and all string-likes.
### torune

The `torune` function converts an integral value to a rune.
### tobyte

The `tobyte` function converts an integer to a byte.
### tofloat

The `tofloat` function converts an `integral` value (e.g. int, byte, rune count) to a float.
### toint

The `toint` function converts a float or an `integral` value (e.g. int, byte, rune count) to an integer. An error is thrown if precision has been lost.
### tobytecount

The `tobytecount` function converts an integer to a byte count. An error is thrown if the provided value is negative.
### torstream

The `torstream` function creates a readable stream from a value. If the value is readable (string, byte-slice, ...) a byte stream is returned. If the value is indexable a stream containing the elements is returned.
### tojson

The `tojson` function returns the JSON representation of an Inox value.  The representation depends on the provided pattern (optional second parameter). If you want to create a custom JSON (string) see the `asjson` function instead.

**examples**

```inox
tojson({a: 1}, %{a: int}) # `{"a":1}`
```
```inox
tojson({a: 1}, %object) # `{"a":{"int__value":1}}`
```
```inox
tojson({a: 1}) # `{"object__value":{"a":{"int__value":1}}}`
```
### topjson

The `topjson` function is equivalent to `tojson` but the JSON output is pretty (formatted).
### asjson

The `asjson` function returns a JSON string created by stringifying the provided Inox value as if it was a JSON value. Only objects, lists, string-like values, integers, floats, boolean, and `nil` are supported. If you want to get the JSON representation of an Inox value see the `tojson` function instead.

**examples**

```inox
asjson({a: {b: 1}}) # {a: {b: 1}}
```
### parse

The `parse` function parses a string based on the specified pattern.

**examples**

```inox
parse!("1", %int)
```
### split

The `split` function slices a string into all substrings separated by sep. If a pattern is given as a second argument each substring is parsed based on it.

**examples**

```inox
split!("a,b", ",")
```
```inox
split!("1,2", ",", %int)
```
```inox
split!("first line\nsecond line", "\n")
```

## Cryptography

### hash_password

The `hash_password` function hashes a password string using the Argon2id algorithm, it returns a string containing: the hash, a random salt and parameters. You can find the implementation in this file: https://github.com/inoxlang/inox/blob/main/internal/globals/crypto.go.

**examples**

```inox
hash_password("password")
# output: 
$argon2id$v=19$m=65536,t=1,p=1$xDLqbPJUrCURnSiVYuy/Qg$OhEJCObGgJ2EbcH0a7oE2sfD1+5T2BPRs8SRWkreE00
```
### check_password

The check_password verifies that a password matches a Argon2id hash.

**examples**

```inox
check_password("password", "$argon2id$v=19$m=65536,t=1,p=1$xDLqbPJUrCURnSiVYuy/Qg$OhEJCObGgJ2EbcH0a7oE2sfD1+5T2BPRs8SRWkreE00")
# output: 
true
```
### sha256

The `sha256` function hashes a string or a byte sequence with the SHA-256 algorithm.

**examples**

```inox
sha256("string")
# output: 
0x[473287f8298dba7163a897908958f7c0eae733e25d2e027992ea2edc9bed2fa8]
```
### sha384

The `sha384` function hashes a string or a byte sequence with the SHA-384 algorithm.

**examples**

```inox
sha384("string")
# output: 
0x[36396a7e4de3fa1c2156ad291350adf507d11a8f8be8b124a028c5db40785803ca35a7fc97a6748d85b253babab7953e]
```
### sha512

The `sha512` function hashes a string or a byte sequence with the SHA-512 algorithm.

**examples**

```inox
sha512("string")
# output: 
0x[2757cb3cafc39af451abb2697be79b4ab61d63d74d85b0418629de8c26811b529f3f3780d0150063ff55a2beee74c4ec102a2a2731a1f1f7f10d473ad18a6a87]
```
### rsa

The rsa namespace contains functions to generate a key pair and encrypt/decrypt using OAEP.
#### rsa.gen_key

The rsa.gen_key function generates a public/private key pair.

**examples**

```inox
rsa.gen_key()
# output: 
#{public: "<key>", private: "<secret key>"}
```
#### rsa.encrypt_oaep

The rsa.encrypt_oaep function encrypts a string or byte sequence using a public key.

**examples**

```inox
rsa.encrypt_oaep("message", public_key)
```
#### rsa.decrypt_oaep

The rsa.decrypt_oaep function decrypts a string or byte sequence using a private key.

**examples**

```inox
rsa.encrypt_oaep(bytes, private_key)
```

## DNS

### dns.resolve

The dns.resolve function retrieves DNS records of the given type.

**examples**

```inox
dns.resolve!("github.com" "A")
```

## Encodings

### b64

The `b64` function encodes a string or byte sequence to Base64.
### db64

The `db64` function decodes a byte sequence from Base64.
### hex

The `hex` function encodes a string or byte sequence to hexadecimal.
### unhex

The `unhex` function decodes a byte sequence from hexadecimal.

## Filesystem

### fs

The fs namespace contains functions to interact with the filesystem.
### fs.mkfile

The fs.mkfile function takes a file path as first argument. It accepts a --readonly switch that causes  the created file to not have the write permission ; and a %readable argument that is the content of the file to create.

**examples**

```inox
fs.mkfile ./file.txt
```
```inox
fs.mkfile ./file.txt "content"
```
### fs.mkdir

The fs.mkdir function takes a directory path as first argument & and optional dictionary literal as a second argument. The second argument recursively describes the content of the directory.

**examples**

```inox
fs.mkdir ./dir_a/
```
```inox
dir_content = :{
    ./subdir_1/: [./empty_file]
    ./subdir_2/: :{  
        ./file: "foo"
    }
    ./file: "bar"
}

fs.mkdir ./dir_b/ $dir_content
```
### fs.read

The fs.read function behaves exactly like the `read` function but only works on files & directories. The content of files is parsed by default, to disable parsing use --raw after the path: a byte slice will be returned instead.  The type of content is determined by looking at the extension.
### fs.read_file

The fs.read function behaves exactly like the `read` function but only works on files. The content is parsed by default, to disable parsing use --raw after the path: a byte slice will be returned instead. The type of content is determined by looking at the extension.
### fs.ls

The fs.ls function takes a directory path or a path pattern as first argument and returns a list of entries, if no argument is provided the ./ directory is used.

**examples**

```inox
fs.ls()
```
```inox
fs.ls ./
```
```inox
fs.ls %./*.json
```
### fs.rename

The fs.rename (fs.mv) function renames a file, it takes two path arguments.  An error is returned if a file already exists at the target path.
### fs.cp

The fs.cp function copies a file/dir at a destination or a list of files in a destination directory, the copy is recursive by default. As you can see this behaviour is not exactly the same as the cp command on Unix. An error is returned if a file or a directory already exists at one of the target paths (recursive).

**examples**

```inox
fs.cp ./file.txt ./file_copy.txt
```
```inox
fs.cp ./dir/ ./dir_copy/
```
```inox
fs.cp [./file.txt, ./dir/] ./dest_dir/
```
### fs.exists

the fs.exists takes a path as first argument and returns a boolean.
### fs.isdir

the fs.isdir function returns true if there is a directory at the given path.
### fs.isfile

the fs.isfile returns true if there is a regular file at the given path.
### fs.remove

the fs.remove function removes a file or a directory recursively.
### fs.glob

the fs.glob function takes a globbing path pattern argument (%./a/... will not work) and returns a list of paths matching this pattern.
### fs.find

The fs.find function takes a directory path argument followed by one or more globbing path patterns,  it returns a directory entry for each file matching at least one of the pattern.

**examples**

```inox
fs.find ./ %./**/*.json
```
### fs.get_tree_data

The fs.get_tree_data function takes a directory path argument and returns a %treedata value  thats contains the file hiearachy of the passed directory.

**examples**

```inox
fs.get_tree_data(./)
```

## Functional Programming

### map_iterable

The `map_iterable` function creates a list by applying an operation on each element of an iterable.

**examples**

```inox
map_iterable([{name: "foo"}], .name)
# output: 
["foo"]
```
```inox
map_iterable([{a: 1, b: 2, c: 3}], .{a,b})
# output: 
[{a: 1, b: 2}]
```
```inox
map_iterable([0, 1, 2], Mapping{0 => "0" 1 => "1"})
# output: 
["0", "1", nil]
```
```inox
map_iterable([97, 98, 99], torune)
# output: 
['a', 'b', 'c']
```
```inox
map_iterable([0, 1, 2], @($ + 1))
# output: 
[1, 2, 3]
```
### filter_iterable

The `filter_iterable` function creates a list by iterating over an iterable and keeping elements that pass a condition.

**examples**

```inox
filter_iterable!(["a", "0", 1], %int)
# output: 
[1]
```
```inox
filter_iterable!([0, 1, 2], @($ >= 1))
# output: 
[1, 2]
```
### get_at_most

The `get_at_most` function gets at most the specified number of elements from an iterable.

**examples**

```inox
get_at_most(1, [])
# output: 
[]
```
```inox
get_at_most(3, ["a", "b"])
# output: 
["a", "b"]
```
```inox
get_at_most(2, ["a", "b", "c"])
# output: 
["a", "b"]
```
### some

The `some` function returns true if and only if at least one element of an iterable passes a condition. For an empty iterable the result is always true.

**examples**

```inox
some(["a", "0", 1], %int)
# output: 
true
```
```inox
some([0, 1, 2], @($ == 'a'))
# output: 
false
```
### all

The `all` function returns true if and only if all elements of an iterable pass a condition. For an empty iterable the result is always true.

**examples**

```inox
all([0, 1, "a"], %int)
# output: 
false
```
```inox
all([0, 1, 2], @($ >= 0))
# output: 
true
```
### none

The `none` function returns true if and only if no elements of an iterable pass a condition. For an empty iterable the result is always true.

**examples**

```inox
none([0, 1, "a"], %int)
# output: 
false
```
```inox
none([0, 1, 2], @($ < 0))
# output: 
true
```
### find

The `find` function searches for items matching a pattern at a given location (a string, an iterable, a directory).

**examples**

```inox
find %`a+` "a-aa-aaa"
# output: 
["a", "aa", "aaa"]
```
```inox
find %./**/*.json ./
# output: 
[./file.json, ./dir/file.json, ./dir/dir/.file.json]
```
```inox
find %int ['1', 2, "3"]
# output: 
[2]
```
### idt

The idt (identity) function takes a single argument and returns it.

## HTML

### html

The html namespace contains functions to create & manipulate HTML nodes.
### html.find

The html.find function finds all elements matching the specified CSS selector in the specified element.

**examples**

```inox
h1_elems = html.find("h1", html<div> <h1>title</h1> </div>)
```
### html.escape

The html.escape function escapes special characters like "<" to become "&lt;". It escapes only five such characters: <, >, &, ' and ".

**examples**

```inox
html.escape("<span></span>")
# output: 
&lt;span&gt;&lt;/span&gt;
```
### html.Node

The html.Node factory function creates a node with a given tag. The first argument is the tag name. The second argument is an object with several optional parameters (properties): id, class, children. Additional parameters depend on the tag: (e.g. href for `<a>`). html.Node is still being developped, for now it only supports additional  parameters for `<a>`. It is recommended to use XML expressions with `html` as the namespace instead of using html.Node:  calling the function and creating the object argument (description) is not efficient.

**examples**

```inox
html.Node("a", {})
# output: 
<a></a>
```
```inox
html.Node("a", {id: "link"})
# output: 
<a id="link"></a>
```
```inox
html.Node("a", {href: /index.html})
# output: 
<a href="/index.html"></a>
```
```inox
html.Node("div", {  html<span>text</span> })
# output: 
<div><span>text</span></div>
```
```inox
html.Node("div", { children: [ html<span>text</span> ] })
# output: 
<div><span>text</span></div>
```

## HTTP

### http

The http namespace contains functions to read, modify & delete HTTP resources. Most functions accept the --insecure option to ignore certificate errors & the --client option to specify an HTTP client to use.
### http.get

The http.get function takes a URL (or host) as first argument and returns an HTTP response. The --insecure options causes the function to ignore certificate errors.

**examples**

```inox
http.get https://example.com/
```
### http.read

The http.read function behaves exactly like the `read` function but only works on HTTP resources. By default the type of content is determined by looking at the Content-Type header. You can specify a content type by adding a mimetype value such as mime"json".

**examples**

```inox
http.read https://jsonplaceholder.typicode.com/posts/1
```
### http.exists

the http.exists takes a URL (or host) as argument, it sends a HEAD request and returns true if the status code is less than 400.
### http.post

The http.post sends a POST request to the specified URL (or host) with the given body value, the body value can be any %readable or serializable object/list. A %mimetype value can be specified to change the value of the Content-Type header.

**examples**

```inox
http.post https://example.com/posts `{"title":"hello"}`
```
```inox
http.post https://example.com/posts {title: "hello"}
```
```inox
http.post https://example.com/posts [ {title: "hello"} ]
```
```inox
http.post https://example.com/posts mime"json" {title:"hello"}
```
### http.patch

The http.patch function works like http.post but sends an HTTP PATCH request instead.
### http.delete

The http.delete function sends an HTTP DELETE request to the specified URL.
### http.Client

The http.Client function creates an HTTP client that can be used in most http.* functions with the --client flag.

**examples**

```inox
http.Client{ save-cookies: true }
```
```inox
http.Client{ insecure: true }
```
```inox
http.Client{
  request-finalization: :{
    https://example.com : { 
      add-headers: {X-API-KEY: env.initial.API_KEY}
    }
  } 
}
```
### http.Server

The http.Server function creates a listening HTTPS server with a given with a given address & handler. The address should be a HTTPS host such as `https://localhost:8080` or `https://0.0.0.0:8080`. The handler can be an function or a Mapping that routes requests.  When you send a request to a server listening on localhost add the --insecure flag to ignore certificate errors. When using filesystem routing modules are reloaded each time files are changed in /routes/. Also for each page render a nonce is added to the `script-src-elem` CSP directive and to all `<script>` elements in the page's HTML.

**examples**

```inox
server = http.Server!(https://localhost:8080, {
    routing: {
        static: /static/
        dynamic: /routes/
    }
})
```
```inox
fn handle(rw http.resp-writer, r http.req){
  rw.write_json({ a: 1 })
}

server = http.Server!(https://localhost:8080, Mapping {
    /hello => "hello"
    %/... => handle
})
```
```inox
fn handle(rw http.resp-writer, r http.req){
    match r.path {
      / {
          rw.write_json({ a: 1 })
      }
      %/... {
        rw.write_headers(http.status.NOT_FOUND)
      }
    }
}

server = http.Server!(https://localhost:8080, handle)
```
### http.FileServer

The http.FileServer creates an HTTP server that serves static file from a given directory.

**examples**

```inox
http.FileServer!(https://localhost:8080, ./examples/static/)
```
### http.servefile


### http.CSP

The http.CSP function creates a Content Security Policy with the passed directives and some default directives: 
all types that are not provided in arguments default to the following:
  - `default-src 'none';`
  - `frame-ancestors 'none';`
  - `frame-src 'none';`
  - `script-src-elem 'self' 'nonce-[page-nonce]>';`
  - `connect-src 'self';`
  - `font-src 'self';`
  - `img-src 'self';`
  - `style-src-elem 'self' 'unsafe-inline';`.

**examples**

```inox
http.CSP{default-src: "'self'"}
```
### http.Result

The `http.Result` function creates an HTTP result that, if returned by a handler, is used by the HTTP server to construct a response.
The function has several parameters that are all optional:
- `status`: a status code, defaults to 200.
- `body` a value that is used to construct the response's body; the Content-Type header is inferred.
- `headers`: an object `{<header name>: <str> | <strings>}`
- `session` an `{id: <hex-encoded id string>}` object that is stored in the session storage if the result is returned by a handler;
  if an empty id is provided it is updated with a random one.

**examples**

```inox
http.Result{status: http.status.BAD_REQUEST}
```

## ID Parsing

### ULID

The `ULID` function parses the string representation of a ULID and returns an `ulid` value. https://github.com/ulid/spec.

**examples**

```inox
ULID("01HNZ7E5R630AD87V7FWSFZ865")
```
### UUIDv4

The `UUIDv4` function parses the string representation of a UUIDV4 and returns an `uuiv4` value. https://en.wikipedia.org/wiki/Universally_unique_identifier#Version_4_(random).

**examples**

```inox
UUIDv4("968011a9-52dc-4816-8527-04b737376471")
```

## Integer Utils

### is_even

`is_even` tells whether the provided integer is even, negative values are allowed.
### is_odd

`is_odd` tells whether the provided integer is odd, negative values are allowed.

## Structured Logging

### log

The log namespace contains functions for structured logging.
### log.add

The log.add function logs an event that is created from the provided record. The log level is specified with the `lvl` property, it defaults to `debug`. The message can be either provided by setting the `msg` property or by adding properties with implicit keys: each implicit-key property will be a single part of the message. ⚠️ It is recommended to use the default level (debug) for high frequency events.

**examples**

```inox
# add a log event of level 'debug' with the message 'user created'
log.add #{"user created"}
```
```inox
# add a log event of level 'debug' with the message 'user created'
log.add #{msg: "user created"}
```
```inox
# add a log event of level 'info' with the message 'user created'
log.add #{lvl: "info", msg: "user created"}
```
```inox
id = 100
# add a log event of level 'debug' with the message 'user 100 created'
# and a field `id: 100`
log.add #{"user", id, "created", id: 100}
```

## Minimum & Maximum

### minof

`minof` returns the minimum value among its arguments, it supports all comparable values.

**examples**

```inox
minof(1, 2)
```
```inox
minof(1ms, 1s)
```
### maxof

`maxof` returns the maximum value among its arguments, it supports all comparable values.

**examples**

```inox
maxof(1, 2)
```
```inox
maxof(1ms, 1s)
```
### minmax

`minmax` returns the minimum and maximum values among its arguments, it supports all comparable values.

**examples**

```inox
assign min max = minmax(1, 2)
```
```inox
assign min max = minmax(1ms, 1s)
```

## Printing

### print

The `print` function prints its arguments with a space ' ' separation. A `\n` character is added at the end.
### fprint

The `fprint` function writes to the provided writer its arguments with a space ' ' separation. A '\n' character is added at the end.

## rand

### rand

The `rand` function generates/picks a random value in a cryptographically secure way. If the argument is a pattern a matching value is returned, if the argument is an indexable an element is picked.

**examples**

```inox
rand(%int(0..10))
# output: 
3
```
```inox
rand(%str("a"+))
# output: 
"aaaaa"
```
```inox
rand(["a", "b"])
# output: 
"b"
```

## Resource Manipulation

### read

`read` is a general purpose function that reads the content of a file, a directory or an HTTP resource. The content is parsed by default, to disable parsing use --raw after the resource's name: a byte slice  will be returned instead. The type of content is determined by looking at the extension for files &  the Content-Type header for HTTP resources.

**examples**

```inox
read ./
# output: 
[
  dir/
  file.txt 1kB 
]

```
```inox
read ./file.txt
# output: 
hello
```
```inox
read ./file.json
# output: 
{"key": "value"}
```
```inox
read https://jsonplaceholder.typicode.com/posts/1
# output: 
{
  "body": "quia et suscipit\nsuscipit recusandae consequuntur expedita....", 
  "id": 1.0, 
  "title": "sunt aut facere repellat provident occaecati excepturi optio reprehenderit", 
  "userId": 1.0
}

```
### create

`create` is a general purpose function that can create a file, a directory or an HTTP resource.

**examples**

```inox
create ./dir/
```
```inox
create ./empty-file.txt
```
```inox
create ./file.txt "content"
```
### update

`update` is a general purpose function that updates an existing resource, it has 2 modes: append and replace. Replace is the default mode.

**examples**

```inox
update ./file.txt append "additional content"
```
```inox
update ./file.txt "new content"
```
```inox
update ./file.txt replace "new content"
```
```inox
update https://example.com/users/100 tojson({name: "foo"})
```
### delete

`delete` is a general purpose function that deletes a resource, deletion is recursive for directories.

**examples**

```inox
delete ./file.txt
```
```inox
delete ./dir/
```
```inox
delete https://example.com/users/100
```
### get

The `get` function loads a resource located at the specified URL. `get` only supports databases for now.

**examples**

```inox
get(ldb://main/users)
```
```inox
get(ldb://main/users/01HNZ7E5R630AD87V7FWSFZ865)
```

## Email Address

### EmailAddress

The `EmailAddress` function parses a RFC 5322 email address and returns a normalized `emailaddr` value. The normalization algorithm depends on the email provider, for example: `john.doe@gmail.com` is normalized  to `johndoe@gmail.com`. The email is not normalized if the provider is not supported, supported providers are  listed here: https://github.com/dimuska139/go-email-normalizer?tab=readme-ov-file#supported-providers. Internationalized email addresses will be supported in the future.

**examples**

```inox
EmailAddress("john.doe@gmail.com")
```

## TCP

### tcp.connect

The tcp.connect function creates a TCP connection to a given host.

**examples**

```inox
conn = tcp.connect!(://example.com:80)

conn.write!("GET / HTTP/1.1\nHost: example.com\n\n")
print tostr(conn.read!())

conn.close()
```

## Time

### ago

The `ago` function returns the current datetime minus the provided duration.

**examples**

```inox
ago(1h)
```
### now

The `now` function returns the current datetime.
### time_since

The `time_since` function returns the time elapsed (duration) since the provided datetime.
### sleep

The `sleep` function pauses the execution for the given duration.

**examples**

```inox
sleep(1s)
```

