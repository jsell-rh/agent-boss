# Agent Definition System Design

**Author:** OpenDispatch Development | **Date:** 2026-03-02  
**Context:** Pre-defined agents for software factory automation  

## Overview

This design defines how to create strongly-opinionated, pre-defined agents that know exactly what they do and how. Agent definitions live in the `.claude/` directory structure, making ignition easier and enabling portable agent roles across projects. The Boss loop controls all agents and serves as the human touchpoint for the software factory.

## Agent Definition System

### Directory Structure

```
.claude/
├── agents/
│   ├── boss.yaml             # Human coordination agent (was overlord)
│   ├── trex.yaml             # Foundational boilerplate generation
│   ├── api.yaml              # API server implementation
│   ├── sdk.yaml              # Multi-language SDK generation
│   ├── cli.yaml              # CLI tool implementation
│   ├── cp.yaml               # Control plane custom behaviors
│   ├── frontend.yaml         # UI/customer-facing frontend
│   ├── cluster.yaml          # Live cluster management
│   └── reviewer.yaml         # Code review and standards
├── factory/
│   ├── ambient-pipeline.yaml    # The stage DAG for Ambient components
│   └── workflows/
│       ├── kind-spec.yaml
│       └── pr-cascade.yaml
├── context/
│   ├── security-standards.md
│   └── coding-guidelines.md
└── settings.json                # Existing allowlist rules
```

### Agent Definition Schema

```yaml
# .claude/agents/reviewer.yaml
apiVersion: agent-boss.io/v1
kind: AgentDefinition
metadata:
  name: reviewer
  role: code-reviewer
spec:
  description: "Reviews code changes across all worktrees, enforces standards"
  responsibilities:
    - "Review PRs for security, performance, and style compliance"
    - "Check test coverage and documentation"
    - "Verify adherence to .claude/context/ standards"
  skills:
    - code-review
    - security-analysis
    - test-coverage
  worktree_pattern: "*-reviewer"
  communication:
    blackboard_section: "### Reviewer"
    status_template: "Reviewer: {{.summary}} — {{.review_count}} reviews, {{.findings}} findings"
  protocols:
    ignition_endpoint: "/spaces/{space}/ignition/Reviewer"
    channel: "/spaces/{space}/agent/Reviewer" 
    required_headers:
      - "X-Agent-Name: Reviewer"
  behavior:
    auto_assign_prs: true
    review_triggers:
      - "PR opened"
      - "commit pushed to PR branch"
    escalation_patterns:
      - "[?BOSS] Critical security finding requires human review"
```

```yaml
# .claude/agents/boss.yaml
apiVersion: agent-boss.io/v1
kind: AgentDefinition
metadata:
  name: boss
  role: coordination
spec:
  description: "Human-agent coordination interface, orchestrates workflows"
  responsibilities:
    - "Monitor pipeline progress across all agents"
    - "Issue sequencing directives based on dependency graph"
    - "Escalate blockers and coordinate resolution"
    - "Interface with human through [?BOSS] escalations"
  skills:
    - workflow-orchestration
    - dependency-management
    - escalation-handling
    - human-interface
  communication:
    blackboard_section: "### Boss"
    status_template: "Boss: {{.summary}} — {{.active_directives}} directives, {{.blocked_agents}} blocked"
  protocols:
    decision_authority: ["sequencing", "reassignment", "escalation"]
    broadcast_capability: true
    human_touchpoint: true
  behavior:
    polling_interval: "30s"
    escalation_threshold: "5m idle"
```

```yaml
# .claude/agents/trex.yaml
apiVersion: agent-boss.io/v1
kind: AgentDefinition
metadata:
  name: trex
  role: foundational-generation
spec:
  description: "Foundational boilerplate code generation for APIs, SDKs, CLIs, Console Plugins"
  responsibilities:
    - "Generate API server boilerplate from TRex templates"
    - "Create initial SDK scaffolding for Go, TypeScript, Python"
    - "Generate CLI command structure and scaffolding"
    - "Generate OpenShift Console Plugin templates"
    - "Maintain and evolve foundational templates upstream"
  skills:
    - code-generation
    - template-engineering
    - go-development
    - typescript-development
    - python-development
    - openshift-console-plugins
  worktree_pattern: "*-trex"
  communication:
    blackboard_section: "### TRex"
    status_template: "TRex: {{.summary}} — {{.templates}} templates, {{.components}} components generated"
  protocols:
    ignition_endpoint: "/spaces/{space}/ignition/TRex"
    channel: "/spaces/{space}/agent/TRex"
    required_headers:
      - "X-Agent-Name: TRex"
  behavior:
    foundational_layer: true
    template_sources: ["rh-trex-ai"]
    auto_scaffold: true
    quality_gates:
      - "all templates compile"
      - "scaffolded code builds"
      - "no hardcoded values in templates"
```

```yaml
# .claude/agents/api.yaml
apiVersion: agent-boss.io/v1
kind: AgentDefinition
metadata:
  name: api
  role: api-implementation
spec:
  description: "Implements API endpoints, gRPC services, produces openapi.yml"
  responsibilities:
    - "Implement REST and gRPC endpoints per spec"
    - "Add authentication and authorization middleware"
    - "Maintain API compatibility and versioning"
    - "Generate accurate openapi.yml specifications"
    - "Provide CRUD operations and basic persistence"
  skills:
    - go-development
    - api-design
    - grpc-implementation
    - middleware-development
    - openapi-generation
  worktree_pattern: "*-api-server"
  communication:
    blackboard_section: "### API"
    status_template: "API: {{.summary}} — PR {{.pr}}, {{.test_count}} tests, {{.phase}}"
  protocols:
    ignition_endpoint: "/spaces/{space}/ignition/API"
    channel: "/spaces/{space}/agent/API"
    required_headers:
      - "X-Agent-Name: API"
  behavior:
    auto_run_tests: true
    lint_on_commit: true
    test_frameworks: ["go test", "integration_testing"]
    depends_on: ["trex"]
    provides_for: ["sdk", "cli"]
    quality_gates:
      - "go fmt clean"
      - "go vet clean" 
      - "golangci-lint clean"
      - "test coverage > 80%"
      - "openapi.yml validates"
```

```yaml
# .claude/agents/sdk.yaml
apiVersion: agent-boss.io/v1
kind: AgentDefinition
metadata:
  name: sdk
  role: sdk-generation
spec:
  description: "Generates multi-language SDKs from API openapi.yml specs"
  responsibilities:
    - "Generate Go, TypeScript, Python SDKs from openapi.yml"
    - "Maintain SDK examples and documentation"
    - "Ensure SDK backward compatibility"
    - "Coordinate with API openapi.yml changes"
  skills:
    - code-generation
    - go-development
    - typescript-development
    - python-development
    - openapi-codegen
  worktree_pattern: "*-sdk"
  communication:
    blackboard_section: "### SDK"
    status_template: "SDK: {{.summary}} — {{.languages}} languages, {{.examples}} examples"
  protocols:
    ignition_endpoint: "/spaces/{space}/ignition/SDK"
    channel: "/spaces/{space}/agent/SDK"
    required_headers:
      - "X-Agent-Name: SDK"
  behavior:
    auto_generate: true
    depends_on: ["api"]
    provides_for: ["frontend"]
    dependency_triggers:
      - "openapi.yml changes"
      - "API implementation commits"
    quality_gates:
      - "all languages build"
      - "examples run successfully"
      - "no breaking changes"
      - "backward compatibility maintained"
```

```yaml
# .claude/agents/cli.yaml
apiVersion: agent-boss.io/v1
kind: AgentDefinition
metadata:
  name: cli
  role: cli-implementation
spec:
  description: "Implements CLI tools from API openapi.yml specifications"
  responsibilities:
    - "Generate CLI commands from openapi.yml endpoints"
    - "Implement authentication and configuration management"
    - "Provide human-friendly command interfaces"
    - "Maintain CLI backward compatibility"
  skills:
    - go-development
    - cli-design
    - cobra-cli
    - openapi-cli-generation
    - user-experience
  worktree_pattern: "*-cli"
  communication:
    blackboard_section: "### CLI"
    status_template: "CLI: {{.summary}} — {{.commands}} commands, {{.features}} features"
  protocols:
    ignition_endpoint: "/spaces/{space}/ignition/CLI"
    channel: "/spaces/{space}/agent/CLI"
    required_headers:
      - "X-Agent-Name: CLI"
  behavior:
    auto_generate: true
    depends_on: ["api"]
    dependency_triggers:
      - "openapi.yml changes"
      - "new API endpoints"
    quality_gates:
      - "all commands build"
      - "help text comprehensive"
      - "authentication works"
      - "config management functional"
```

```yaml
# .claude/agents/cp.yaml
apiVersion: agent-boss.io/v1
kind: AgentDefinition
metadata:
  name: cp
  role: control-plane
spec:
  description: "Implements control plane custom behaviors via gRPC API consumption"
  responsibilities:
    - "Watch API server for data changes via gRPC streams"
    - "Implement custom business logic and behaviors"
    - "Define 'what happens when your data changes'"
    - "Reconcile desired vs actual state"
    - "Interface with Kubernetes operators when applicable"
  skills:
    - go-development
    - grpc-streaming
    - kubernetes-operators
    - reconciliation-patterns
    - custom-business-logic
  worktree_pattern: "*-control-plane"
  communication:
    blackboard_section: "### CP"
    status_template: "CP: {{.summary}} — {{.watchers}} watchers, {{.reconcilers}} reconcilers"
  protocols:
    ignition_endpoint: "/spaces/{space}/ignition/CP"
    channel: "/spaces/{space}/agent/CP"
    required_headers:
      - "X-Agent-Name: CP"
  behavior:
    depends_on: ["api"]
    consumes_grpc: true
    custom_behavior_layer: true
    quality_gates:
      - "gRPC connectivity verified"
      - "watch streams functional"
      - "reconciliation logic tested"
      - "error handling comprehensive"
```

```yaml
# .claude/agents/frontend.yaml
apiVersion: agent-boss.io/v1
kind: AgentDefinition
metadata:
  name: frontend
  role: customer-ui
spec:
  description: "Implements customer-facing UI consuming gRPC APIs via generated SDKs"
  responsibilities:
    - "Build customer-facing user interfaces"
    - "Consume APIs via generated TypeScript/JavaScript SDKs"
    - "Implement responsive design and user experience"
    - "Handle authentication and session management"
    - "Provide real-time updates via gRPC streams"
  skills:
    - typescript-development
    - react-development
    - grpc-web
    - ui-ux-design
    - responsive-design
    - authentication-flows
  worktree_pattern: "*-frontend"
  communication:
    blackboard_section: "### Frontend"
    status_template: "Frontend: {{.summary}} — {{.components}} components, {{.pages}} pages"
  protocols:
    ignition_endpoint: "/spaces/{space}/ignition/Frontend"
    channel: "/spaces/{space}/agent/Frontend"
    required_headers:
      - "X-Agent-Name: Frontend"
  behavior:
    depends_on: ["sdk", "api"]
    consumes_grpc: true
    customer_facing: true
    quality_gates:
      - "TypeScript builds without errors"
      - "UI components render correctly"
      - "gRPC connectivity works"
      - "authentication flow functional"
      - "responsive design verified"
```

```yaml
# .claude/agents/cluster.yaml
apiVersion: agent-boss.io/v1
kind: AgentDefinition
metadata:
  name: cluster
  role: cluster-management
spec:
  description: "Manages live cluster deployments, local kind or OpenShift"
  responsibilities:
    - "Deploy applications to Kubernetes/OpenShift clusters"
    - "Manage cluster configurations and secrets"
    - "Run end-to-end tests against live deployments"
    - "Monitor cluster health and application status"
    - "Handle cluster-specific networking and ingress"
  skills:
    - kubernetes-administration
    - openshift-administration
    - kubectl-oc-cli
    - yaml-manifests
    - ingress-networking
    - e2e-testing
  worktree_pattern: "*-cluster"
  communication:
    blackboard_section: "### Cluster"
    status_template: "Cluster: {{.summary}} — {{.cluster_type}} cluster, {{.services}} services"
  protocols:
    ignition_endpoint: "/spaces/{space}/ignition/Cluster"
    channel: "/spaces/{space}/agent/Cluster"
    required_headers:
      - "X-Agent-Name: Cluster"
  behavior:
    depends_on: ["api", "frontend", "cp"]
    live_environment: true
    deployment_target: true
    quality_gates:
      - "cluster connectivity verified"
      - "manifests apply successfully"
      - "services are healthy"
      - "e2e tests pass"
      - "ingress routing functional"
```

### Factory Pipeline Integration

```yaml
# .claude/factory/ambient-pipeline.yaml
apiVersion: factory.agent-boss.io/v1
kind: FactoryPipeline
metadata:
  name: ambient-component
  description: "Standard pipeline for Ambient platform components"
spec:
  stages:
    - name: spec-review
      description: "Review component specification for completeness"
      agents: [reviewer]
      gates: [spec-approved]
      timeout: "1h"
      
    - name: foundational-generation
      description: "Generate foundational boilerplate from TRex templates"
      agents: [trex]
      depends_on: [spec-review]
      gates: [templates-generated, scaffolding-complete]
      timeout: "30m"
      
    - name: api-implementation  
      description: "Implement API endpoints, generate openapi.yml"
      agents: [api]
      depends_on: [foundational-generation]
      gates: [api-tests-pass, lint-clean, openapi-valid]
      timeout: "4h"
      
    - name: parallel-generation
      description: "Generate SDK and CLI from openapi.yml concurrently"
      agents: [sdk, cli]
      depends_on: [api-implementation]
      gates: [sdk-builds, cli-builds, examples-pass]
      timeout: "2h"
      concurrent: true
      
    - name: parallel-behaviors
      description: "Implement custom behaviors concurrently"
      agents: [cp, frontend]
      depends_on: [parallel-generation]
      gates: [cp-reconcilers-pass, ui-tests-pass, grpc-streams-work]
      timeout: "3h"
      concurrent: true
      
    - name: integration-review
      description: "Review all components before deployment"
      agents: [reviewer]
      depends_on: [parallel-behaviors]
      gates: [integration-approved, security-pass]
      timeout: "1h"
      
    - name: deployment
      description: "Deploy to cluster and run full E2E tests"
      agents: [cluster]
      depends_on: [integration-review]
      gates: [e2e-pass, performance-acceptable, cluster-healthy]
      timeout: "1h"
      
    - name: boss-coordination
      description: "Boss loop monitors and coordinates all stages"
      agents: [boss]
      runs_throughout: true
      responsibilities: [sequencing, escalation, human-interface]
      
  rollback_strategy: "stage_boundary"
  escalation_policy: "boss_escalation_after_2_failures"
  concurrency_model: "concurrent_where_possible"
```

## Software Factory Architecture

The factory implements the dependency flow shown in the proposal-agent-boss-ambient.md:

```mermaid
graph TB
    subgraph "Boss Loop (Human Touchpoint)"
        BOSS[Boss Agent<br/>Coordination & Escalation]
    end
    
    subgraph "Foundational Layer"
        SPEC[Kind Spec<br/>YAML] --> REVIEW[Reviewer<br/>Spec Approval]
        REVIEW --> TREX[TRex<br/>Boilerplate Generation]
    end
    
    subgraph "API Layer"
        TREX --> API[API<br/>Endpoints + openapi.yml]
    end
    
    subgraph "Generation Layer (Concurrent)"
        API --> SDK[SDK<br/>Multi-language]
        API --> CLI[CLI<br/>Commands]
    end
    
    subgraph "Custom Behavior Layer (Concurrent)"
        SDK --> FE[Frontend<br/>Customer UI]
        API --> CP[Control Plane<br/>Business Logic]
    end
    
    subgraph "Integration Layer"
        FE --> REVIEW2[Reviewer<br/>Integration Check]
        CP --> REVIEW2
    end
    
    subgraph "Deployment Layer"
        REVIEW2 --> CLUSTER[Cluster<br/>Live Deployment]
    end
    
    BOSS -.->|monitors| API
    BOSS -.->|coordinates| SDK
    BOSS -.->|coordinates| CLI
    BOSS -.->|coordinates| FE
    BOSS -.->|coordinates| CP
    BOSS -.->|escalates| REVIEW2
    BOSS -.->|manages| CLUSTER
    
    style BOSS fill:#d29922,color:#fff
    style TREX fill:#4d9eff,color:#fff
    style API fill:#3fb950,color:#fff
    style REVIEW fill:#a371f7,color:#fff
    style REVIEW2 fill:#a371f7,color:#fff
```

### Key Architectural Principles

1. **TRex Foundation**: All components start with TRex-generated boilerplate, moving templating upstream
2. **API-Driven**: The API agent produces openapi.yml that drives SDK and CLI generation
3. **Concurrent Generation**: SDK and CLI work in parallel once API is stable
4. **Custom Behavior Separation**: CP and Frontend implement interesting custom behaviors
5. **Boss Loop Control**: Human instructions flow through the Boss agent to all other agents
6. **Worktree Isolation**: Each agent has its own worktree and can work independently

## Enhanced Ignition Process

### Current Flow
```
Agent → GET /spaces/{space}/ignition/{agent} → Generic context
```

### Enhanced Flow with Agent Definitions
```
Agent → GET /spaces/{space}/ignition/{agent}?project={project_path}
      ↓
Coordinator loads .claude/agents/{agent}.yaml
      ↓  
Generates role-specific ignition with:
  - Agent responsibilities and skills from YAML
  - Project context from .claude/context/
  - Worktree setup based on worktree_pattern
  - Communication templates from definition
  - Factory pipeline stages relevant to this agent
      ↓
Agent receives comprehensive, role-specific context
```

### Enhanced Ignition Response

```markdown
# Agent Ignition: API (Enhanced)

You are **API**, an API implementation agent working in workspace **sdk-backend-replacement**.

## Your Role (from .claude/agents/api.yaml)

**Description:** Implements API endpoints, gRPC services, produces openapi.yml

**Core Responsibilities:**
- Implement REST and gRPC endpoints per spec
- Add authentication and authorization middleware  
- Maintain API compatibility and versioning
- Generate accurate openapi.yml specifications
- Provide CRUD operations and basic persistence

**Skills:** go-development, api-design, grpc-implementation, middleware-development, openapi-generation

## Worktree Setup
- **Pattern:** `*-api-server`
- **Current:** `/home/user/projects/platform/platform-api-server`
- **Branch:** `pr/multi-component-grpc-integration`

## Quality Gates (Auto-enforced)
- ✅ go fmt clean
- ✅ go vet clean  
- ✅ golangci-lint clean
- 🎯 test coverage > 80% (current: 81 tests)
- 🎯 openapi.yml validates

## Factory Pipeline Context
You are currently in stage **api-implementation** of the ambient-component pipeline:
- ✅ **spec-review** (completed)
- ✅ **foundational-generation** (completed - TRex boilerplate ready)
- 🔄 **api-implementation** (in progress) ← **YOU ARE HERE**
- ⏳ **parallel-generation** (waiting - SDK & CLI concurrent)
- ⏳ **parallel-behaviors** (waiting - CP & Frontend concurrent)
- ⏳ **integration-review** (waiting)
- ⏳ **deployment** (waiting)

**Stage Gates:** api-tests-pass, lint-clean, openapi-valid  
**Timeout:** 4h remaining  
**Dependencies:** SDK and CLI agents will generate from your openapi.yml
**Provides For:** SDK, CLI (via openapi.yml), CP and Frontend (via gRPC)

## Boss Coordination
The Boss agent is monitoring your progress and will:
- Coordinate sequencing with downstream agents
- Escalate blockers via `[?BOSS]` pattern
- Handle human touchpoint for complex decisions

## Communication Templates
- **Status Template:** `API: {{.summary}} — PR {{.pr}}, {{.test_count}} tests, {{.phase}}`
- **Escalation Pattern:** `[?BOSS] API blocker requires Boss intervention: {{.issue}}`

## Project Context
- **Security Standards:** /claude/context/security-standards.md (Bearer token parsing)
- **Coding Guidelines:** /claude/context/coding-guidelines.md  
- **TRex Boilerplate:** Available in worktree from foundational-generation stage
- **Current PR:** #748 (pr/multi-component-grpc-integration)
```

## Benefits

This agent definition system addresses key gaps from the proposal:

### 1. **Spec-First Workflow**
- Agent definitions enforce role boundaries and responsibilities
- Factory pipelines ensure spec-review stage before implementation
- Quality gates prevent progression without meeting standards

### 2. **Consistent Ignition**  
- Agents get role-appropriate context automatically
- Worktree setup is standardized by pattern matching
- Communication templates ensure consistent status formatting

### 3. **Factory Integration**
- Pipeline stages map directly to agent capabilities
- Dependencies are explicit and enforced
- Timeout and escalation policies prevent stalls

### 4. **Cross-Project Reuse**
- Agent definitions are portable across spaces
- Skills and behaviors transfer to new projects
- Factory pipelines can be composed from standard agents

### 5. **Stronger Opinions**
- Each agent knows exactly what it does and how
- Quality gates are enforced automatically  
- Escalation patterns are predefined and consistent

## Implementation Path

1. **Phase 1:** Create `.claude/agents/` directory with core agent definitions
2. **Phase 2:** Enhance ignition endpoint to load and apply agent definitions  
3. **Phase 3:** Implement factory pipeline loading and stage enforcement
4. **Phase 4:** Add quality gate automation and rollback policies

This transforms OpenDispatch from a coordination tool into a true software factory where agents are well-defined, reusable components that can be composed into different workflows based on the work at hand.