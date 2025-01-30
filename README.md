# lspwatch

`lspwatch` is a configurable stdin/stdout proxy for the [Language Server Protocol (LSP)](https://microsoft.github.io/language-server-protocol/) with built-in observability integrations. `lspwatch` produces telemetry for the language server and its LSP request/response communication, and exports it to your observability backend.

<div align="center"><img align="center" width="780" alt="Screenshot 2025-01-18 at 7 25 44 PM" src="https://github.com/user-attachments/assets/119e046f-8c09-41ea-81db-ff45e9ec8d27" /></div>

<br/>

`lspwatch` can calculate and export:

* **Request duration**: how long it takes for the language server to respond to code completion, hover, go-to-definition, etc requests from the editor.

    * Metric name: `lspwatch.request.duration`. Tagged with `method`--the LSP method name (e.g `textDocument/completion`).

* **Language server resident-set size (RSS)**: how much memory the language server is actively using.

    * Metric name: `lspwatch.server.rss`.

Users can optionally choose to tag metrics with:

* `language_server`: name of language server binary (e.g `clangd`).
* `user`: username on the machine.
* `os`: operating system.
* `ram`: total amount of RAM on the machine.

## Why?

If you work on a sufficiently complex typed codebase, you'll find that language support features within modern editors like code completion, diagnostics, suggestions, etc. can become considerably slow. Much of the universally-hated sluggishness of code editors is due in large part to these advanced language features. To provide codebase-wide awarness, editors rely on language servers to provide answers to code queries by continually scanning the codebase and maintaining internal state representations.

To quantify this sluggishness, we can monitor how editors interact with language servers. The duration of an LSP request can tell us how responsive an editor feels. More specifically, this metric can tell us how much (likely idle) time the developer spends waiting for their editor to respond to their edit.

What we essentially have is a **real-time, constant, and accurate indicator of developer experience and productivity**. I have worked on DevEx teams with access to this kind of data, and it's proven to be essential for managing the local development experience at scale.

With `lspwatch`, you get metrics related to the responsiveness of your editor, tagged with contextual details to help with analysis. You can split your metrics by machine configuration, operating system, even down to the user, and more! `lspwatch` can help you with:

* **Monitoring and improving modularization in your codebase**: The nature of the dependency graph will impact the performance of language servers.
* **Monitoring the impact of language version upgrades on the coding experience**: Any immediate improvements or regressions in language servers will can be noticed and acted on quickly.
* **Identifying potentially misconfigured local development setups or faulty machines**.
* **Spotting general usage trends in code editors**.

## Installation

TODO

## Usage

`lspwatch` is an executable that stands in place of the language server. If your language server is invoked using e.g `clangd ...`, then you can run `lspwatch -- clangd ...`.

Every editor that supports LSP will have a way to configure the  command used to invoke the language server. Reference the documenation of your editor or language extension for details.

TODO: Show examples.

## Configuration

`lspwatch` can be configured with a YAML file. Use the `--config` or `-c` flag to specify the path.

TODO
