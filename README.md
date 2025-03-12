<div align="center">
<h1>
  <div class="image-wrapper" style="display: inline-block;">
    <picture>
      <source media="(prefers-color-scheme: dark)" alt="logo" height="200" srcset="https://github.com/user-attachments/assets/8979515b-e38c-4b3c-adf4-bb0b2e5c8594" style="display: block;">
      <source media="(prefers-color-scheme: light)" alt="logo" height="200" srcset="https://github.com/user-attachments/assets/c6cc34d5-44a5-4323-83b0-3ce45809499e" style="display: block;">
      <img alt="Shows my svg">
    </picture>
  </div>
  <a href="https://codecov.io/gh/noredeen/lspwatch" > 
    <img src="https://codecov.io/gh/noredeen/lspwatch/graph/badge.svg?token=M174K60D0U"/> 
  </a>
  <a href="https://goreportcard.com/report/github.com/noredeen/lspwatch" > 
    <img src="https://goreportcard.com/badge/github.com/noredeen/lspwatch"/>
  </a>
</h1>
</div>

`lspwatch` is a configurable stdin/stdout proxy for the [Language Server Protocol (LSP)](https://microsoft.github.io/language-server-protocol/) with built-in observability integrations. `lspwatch` produces telemetry for the language server and its LSP request/response communication, and exports it to your observability backend.

<div align="center">
<h1>
  <div class="image-wrapper" style="display: inline-block;">
    <picture>
      <source media="(prefers-color-scheme: dark)" width="1060" srcset="https://github.com/user-attachments/assets/309e5c83-4ee7-442b-91a6-b975ba5e2035" style="display: block; margin: auto;">
      <source media="(prefers-color-scheme: light)" width="1060" srcset="https://github.com/user-attachments/assets/dad55082-4dc9-4809-8fa0-7a8a9609b25f" style="display: block; margin: auto;">
      <img alt="Shows my svg">
    </picture>
  </div>
</h1>
</div>

`lspwatch` can calculate and export:

* **Request duration**: how long it takes for the language server to respond to code completion, hover, go-to-definition, etc requests from the editor.

    * Metric name: `lspwatch.request.duration`. Tagged with `method`--the LSP method name (e.g `textDocument/completion`).

* **Language server resident-set size (RSS)**: how much memory the language server is actively using.

    * Metric name: `lspwatch.server.rss`.

Users can optionally choose to tag metrics with:

* `language_server`: name of language server.
r binary (e.g `clangd`).
* `user`: username on the machine.
* `os`: operating system.
* `ram`: total amount of RAM on the machine.

## Why?

If you work on a sufficiently complex typed codebase, you'll find that language support features within modern editors like code completion, diagnostics, suggestions, etc. can become considerably slow. Much of the universally hated sluggishness of code editors is due in large part to these advanced language features. To provide codebase-wide awarness, editors rely on language servers to provide answers to code queries by scanning the codebase and maintaining internal state representations.

To quantify this sluggishness, we can monitor how editors interact with language servers. The duration of an LSP request can tell us how responsive an editor feels. More specifically, this metric can tell us how much (likely idle) time the developer spends waiting for their editor to respond to their edit.

What we essentially have is a **real-time, constant, and accurate indicator of developer experience and productivity**. I have worked on DevEx teams with access to this kind of data--it's proven essential for managing the local development experience at scale.

With `lspwatch`, you get metrics related to the responsiveness of your editor, tagged with contextual details to help with analysis. You can split your metrics by machine configuration, operating system, even down to the user, and more! `lspwatch` can help you with:

* **Monitoring and improving modularization in your codebase**: The nature of the dependency graph will impact the performance of language servers.
* **Monitoring the impact of language version upgrades on the coding experience**: Any immediate improvements or regressions in language server performance can be noticed and acted on quickly.
* **Identifying potentially misconfigured local development setups or faulty machines**.
* **Spotting general usage trends in code editors**.

## Installation

TODO

## Usage

`lspwatch` is an executable that transparently stands in place of the language server. `lspwatch` operates in one of two modes:

1. **`command` mode**: For running one-time language server queries like `gopls stats`, where the command exits after returning results. Editors often use such queries to gather static metadata.
2. **`proxy` mode**: For long-running language server sessions where LSP messages are continuously exchanged. This is the  typical usage of the language server.

`lspwatch` will automatically choose the correct mode, but you can also specify it using the `--mode` flag (e.g `lspwatch --mode command -- gopls stats`).

Generally, to use `lpswatch` for instrumenting your language server, you will need to replace the command your code editor invokes when running the language server. For example, instead of running `gopls <args>`, your editor should run `lspwatch -- gopls <args>`. Most LSP-equipped editors will have a way to configure this. Some verified examples are included below, but reference the documentation of your editor or language extension for instructions.

<details>
<summary><b>(Golang) Neovim + <code>nvim-lspconfig</code></b></summary>
<br/>

`/path/to/instrumented_gopls`:
```bash
#!/bin/bash
lspwatch -- gopls "$@"
```

`init.lua`:
```lua
gopls = {
  cmd = {
    "/path/to/instrumented_gopls"
  }
}
```
</details>

<details>
<summary><b>(Golang) VSCode + vscode-go</code></b></summary>
<br/>

`/path/to/instrumented_gopls`:
```bash
#!/bin/bash
lspwatch -- gopls "$@"
```

`.vscode/settings.json`:
```json
{
    "go.alternateTools": {
        "gopls": "/path/to/instrumented_gopls"
    }
}
```
</details>

## Configuration

`lspwatch` can be configured with a YAML file. Use the `--config` or `-c` flag to specify the path.

TODO

## Backlog

- [ ] File rotation for logs and OTel local exports.
- [ ] Support for `tsserver` messages.
- [ ] New metric: Number of language server crashes.
