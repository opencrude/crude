# CRUDE: A DAG-Based AI Agent Architecture for System Operations

## Overview

CRUDE is an open-source CLI agent framework written in Go that enables AI models to perform real system administration tasks safely and reliably. It decomposes all system operations into five atomic primitives — **Create, Read, Update, Delete, Execute** — and orchestrates them through a directed acyclic graph (DAG) execution engine with built-in conversational resolution.

Unlike conventional AI agent frameworks that rely on native function calling and single-pass planning, CRUDE enforces a **grounding-first** approach: the agent must observe system state before it plans, and it must plan before it acts. This makes it dramatically more reliable than raw LLM function calling, especially with smaller, cost-effective models.

## The Problem

Current AI agent architectures suffer from a fundamental flaw: they trust the language model to generate a correct plan, with correct parameters, in the correct order, in a single inference pass. This works for toy demos. It falls apart in production.

Consider an insurance chatbot built on an 80B parameter model with native function calling. It hallucinates policy numbers. It assumes customer status without checking. It sequences operations incorrectly. It breaks in ways that are unpredictable and hard to debug. Scaling up the model helps marginally but doesn't fix the structural problem — you're still asking a probabilistic text generator to be a deterministic execution engine.

The industry response has been to add more guardrails around the same broken architecture: output validators, retry loops, prompt engineering. CRUDE takes a different approach. Instead of constraining a bad architecture, it replaces it with one designed for reliability from the ground up.

## Core Principles

### 1. Five Atomic Operations

Every action a system administrator performs reduces to one of five primitives:

| Operation | Description | Examples |
|-----------|-------------|----------|
| **Create** | Bring something new into existence | Write files, create users, spawn services, generate configs |
| **Read** | Observe current state without modification | Check logs, query status, read configs, list processes |
| **Update** | Modify something that already exists | Edit configs, patch files, change permissions, update records |
| **Delete** | Remove something from existence | Remove files, kill processes, revoke access, drop records |
| **Execute** | Trigger a process or command | Run scripts, call APIs, restart services, deploy artifacts |

This is not a metaphor. It is a literal constraint on what the agent can do. Every tool the agent has access to is classified as one of these five types. There is nothing else. If a task cannot be decomposed into CRUDE operations, it cannot be performed — and that's a feature, not a limitation.

### 2. No Assumptions — Read First

The single most important design decision in CRUDE is this: **the agent is prohibited from assuming anything about system state.** Before it plans, before it acts, it must Read.

When a user request arrives, the LLM is constrained to emit Read operations first. It must iteratively gather information — inspecting files, querying services, checking configurations — until it has enough grounded context to build a plan. This is enforced at the protocol level, not through prompt engineering.

This mirrors what separates a senior sysadmin from a junior one. The junior hears "the server is down" and starts restarting things. The senior checks logs, checks network, checks recent deployments, *then* acts. CRUDE encodes that discipline structurally.

### 3. DAG-Based Execution

Plans in CRUDE are not linear scripts. They are directed acyclic graphs where each node represents a CRUDE operation and edges represent dependencies. This gives the engine several properties that linear execution lacks:

- **Parallel execution**: Independent branches run concurrently.
- **Partial completion**: If one branch fails, others can still succeed. The engine knows exactly what worked and what didn't.
- **Dependency encoding**: The topology of the graph *is* the execution order. No separate scheduler needed.
- **Incremental refinement**: The DAG can be extended or pruned as new information arrives.

### 4. State Propagation with Leaf-to-Root Collapse

Every node in the DAG has one of five states:

| State | Meaning |
|-------|---------|
| **Pending** | Not yet started; waiting on dependencies |
| **Executing** | Currently running |
| **Successful** | Completed without error |
| **Error** | Failed; includes error context |
| **Blocking** | Cannot proceed without external input |

The engine resolves the DAG from the leaves inward. Leaf nodes are the most concrete — they either succeed or fail. Once a leaf resolves, its result propagates upward to its parent. A parent with all children successful collapses to successful. A parent with a failed child may collapse to error, trigger a retry, or escalate to blocking.

This is analogous to how proof assistants resolve goal trees — work from the simplest subgoals back to the root — or how build systems like Bazel resolve dependency graphs.

### 5. Conversational Resolution

When a node enters the Blocking state, the engine doesn't just fail. It generates a precise description of *what* is blocked and *why*, then presents this to the user (or to another system) as a question. The blocking node contains enough context to make the question specific and actionable.

This creates a natural feedback loop:

1. Engine encounters a blocker: "I need to configure the SSL certificate for domain X, but I found three certificates in /etc/ssl/certs and I don't know which one to use."
2. User provides input: "Use the one expiring in 2026."
3. Engine resolves the blocker, updates the DAG, continues execution.
4. New blocker? Loop again.

Each iteration narrows the uncertainty. The conversation isn't chatting — it's resolving specific decision points in the execution graph.

## Architecture

### Request Processing Pipeline

```
User Request
    │
    ▼
┌─────────────────────────┐
│  Grounding Phase         │
│  (Read-only)             │
│                          │
│  • LLM emits Read ops    │
│  • Embedding engine       │
│    decomposes query       │
│  • Cross-reference with   │
│    system observations    │
│  • Iterate until context  │
│    is sufficient          │
└────────────┬────────────┘
             │
             ▼
┌─────────────────────────┐
│  Planning Phase          │
│                          │
│  • LLM generates JSON    │
│    DAG from grounded      │
│    context                │
│  • Parser validates DAG   │
│    structure              │
│  • Operations mapped to   │
│    CRUDE primitives       │
└────────────┬────────────┘
             │
             ▼
┌─────────────────────────┐
│  Execution Engine        │
│                          │
│  • Traverses DAG          │
│  • Runs operations        │
│  • Propagates state       │
│  • Collapses leaf→root    │
│                          │
│  ┌─── Blocking? ────┐   │
│  │                    │   │
│  ▼                    │   │
│  Conversation ────────┘   │
│  (resolve + continue)     │
└────────────┬────────────┘
             │
             ▼
         Result
  (per-node status + output)
```

### Grounding via Embedding + Decay

The grounding phase uses an embedding engine to decompose the user's query into semantic components and cross-reference them with the results of Read operations. This process — referred to as "decay" — progressively breaks down the abstract request into concrete, observable facts about the system.

For example, a request like "make sure the web server is configured correctly" would decay into:
- What web server is installed? (Read)
- What is its current configuration? (Read)
- What ports is it listening on? (Read)
- Are there any error logs? (Read)
- What does "correctly" mean in this context? (may require Blocking → ask user)

Only after the grounding phase produces sufficient context does the LLM generate the execution plan.

### JSON DAG Representation

The LLM outputs a JSON structure representing the task graph. Each node specifies:

```json
{
  "id": "node_1",
  "operation": "Update",
  "description": "Set server_name to example.com in nginx.conf",
  "target": "/etc/nginx/nginx.conf",
  "depends_on": ["node_0"],
  "parameters": {
    "field": "server_name",
    "value": "example.com"
  }
}
```

The parser validates this structure, ensures all dependencies form a valid DAG (no cycles), and maps each operation to its CRUDE implementation before handing it to the execution engine.

### Model Agnosticism

CRUDE does not rely on native function calling. The LLM is used for two things:

1. **Grounding**: Deciding what to Read and interpreting results.
2. **Planning**: Generating the JSON DAG from grounded context.

Both of these are structured text generation tasks that any capable model can perform. This means CRUDE works with:

- Small, fast models (7B–14B) for simple tasks
- Larger models for complex planning
- Any OpenAI-compatible API (Ollama, vLLM, OpenRouter, llama.cpp, etc.)
- No vendor lock-in whatsoever

In practice, smaller models with more active parameters (e.g., mixture-of-experts architectures) often outperform larger dense models in this framework, because the structured protocol compensates for the model's limitations. The model doesn't need to be perfect — it just needs to be good enough to generate valid JSON DAGs from well-grounded context.

## Why Go

- **Single binary distribution**: `curl | sh` install, no runtime dependencies.
- **Native concurrency**: Goroutines and channels map directly to parallel DAG execution. Each node is a goroutine; state transitions flow through channels; blocking nodes literally block.
- **Infrastructure ecosystem**: Go is the language of Terraform, Consul, and most modern infrastructure tooling. The target audience already has it installed.
- **Fast compilation**: Iterate quickly, ship cross-platform binaries from CI.
- **Strong typing**: The DAG structure, node states, and CRUDE operations are all type-safe.

## Pluggable CRUDE Backends

The five operations are defined as a Go interface. Implementations are pluggable:

| Backend | Use Case |
|---------|----------|
| **Local** | Direct system operations on the host |
| **SSH** | Remote server management |
| **API** | Generic HTTP/REST operations |
| **Custom** | Users implement the interface for their environment |

This means the same agent, the same DAG engine, and the same conversation loop work whether you're managing a single laptop or a fleet of remote servers.

## Safety Model

CRUDE treats safety as a structural property, not an afterthought:

- **Capability-based permissions**: Each CRUDE backend declares what it can and cannot do. The agent cannot exceed its granted capabilities.
- **Confirmation gates**: Destructive operations (Delete, and certain Execute calls) require explicit user confirmation before proceeding. This is configurable per-operation and per-backend.
- **Audit trail**: Every operation is logged with its full context — what was done, why, what the system state was before and after, and what the LLM's reasoning was. Intent is logged alongside action.
- **Rollback support**: Where possible, operations record enough state to be reversed. The DAG structure makes it clear which operations need to be rolled back if a later step fails.
- **Read-first enforcement**: The agent literally cannot skip the grounding phase. This prevents the most common class of agent failures — acting on hallucinated assumptions.

## Comparison to Existing Frameworks

| Feature | LangChain / CrewAI | ReAct Agents | Claude Code / Codex | **CRUDE** |
|---------|-------------------|--------------|---------------------|-----------|
| Execution model | Linear chains | Think→Act loop | Shell access | DAG with state propagation |
| Planning | Single-pass | Step-by-step | Implicit | Grounded, then planned |
| Failure handling | Retry or crash | Retry | Retry | Per-branch, with escalation |
| Requires function calling | Yes | Yes | No (shell) | No |
| Model requirements | Large, capable | Large, capable | Large, capable | Works with small models |
| Partial completion | No | No | Partial | Yes, per-branch |
| Conversation on failure | Limited | Limited | Yes | Structured, per-node |
| Safety model | Prompt-based | Prompt-based | Sandboxed | Capability + structural |

## Vision

The premise is simple: a sysadmin's job is CRUDE operations. An AI agent that can safely and reliably perform CRUDE operations, with human oversight at decision points, can do anything a sysadmin can.

CRUDE is not another wrapper around an LLM with shell access. It is a principled execution engine that uses LLMs for what they're good at — understanding intent and generating structured plans — while handling execution, state management, error recovery, and safety through deterministic, auditable code.

The goal is a CLI tool that you point at a system and say:

```bash
crude "set up nginx with SSL for example.com"
```

And it grounds, plans, executes, asks when it's stuck, and gets the job done.
