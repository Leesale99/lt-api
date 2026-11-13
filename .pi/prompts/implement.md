---
description: Scout recon → go-worker implements the task, chained
argument-hint: "<task>"
---
Use the subagent tool with the chain parameter to execute this workflow:

1. First, use the "scout" agent to find all code relevant to: $@
2. Then, use the "go-worker" agent to implement "$@" using the recon context from the previous step (use {previous} placeholder). go-worker applies changes directly and runs tests before reporting.

Execute as a chain, passing output between steps via {previous}. Do NOT implement anything yourself — delegate both steps. When done, give me the go-worker's final report verbatim, plus a one-line note on what scout found.
