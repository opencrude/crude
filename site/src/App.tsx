import { useEffect, useRef, useState, type ComponentType, type ReactNode } from "react";
import {
  ArrowRight,
  ArrowUpRight,
  Bot,
  Check,
  Command,
  Copy,
  Cpu,
  GitCompareArrows,
  Menu,
  MonitorDown,
  PanelsTopLeft,
  Puzzle,
  Settings2,
  ShieldCheck,
  SquareTerminal,
  X,
} from "lucide-react";
import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from "@/components/ui/accordion";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

const REPOSITORY = "https://github.com/opencrude/crude";
const RELEASES = `${REPOSITORY}/releases`;
const DOCS = `${REPOSITORY}/blob/master/docs/README.md`;
const AGENTS = `${REPOSITORY}/blob/master/AGENTS.md`;
const BUILD = `${REPOSITORY}#build-from-source`;
const LICENSE = `${REPOSITORY}/blob/master/LICENSE-APACHE`;
const NOTICE = `${REPOSITORY}/blob/master/NOTICE`;
const ISSUES = `${REPOSITORY}/issues`;

const LINUX_COMMAND = `# Download the installer, read it, then run it.
curl -fsSL ${RELEASES}/latest/download/install.sh -o install.sh
bash install.sh --accept-beta-risk`;

const WINDOWS_COMMAND = `# Download the installer, read it, then run it.
Invoke-WebRequest ${RELEASES}/latest/download/install.ps1 -OutFile install.ps1
.\\install.ps1 -AcceptBetaRisk`;

const SHOT_WIDTH = 1920;
const SHOT_HEIGHT = 1056;

type Icon = ComponentType<{ className?: string }>;

type Scene = {
  value: string;
  label: string;
  icon: Icon;
  title: string;
  body: string;
  points: string[];
  image: string;
  alt: string;
};

const TOUR: Scene[] = [
  {
    value: "workspace",
    label: "Workspace",
    icon: PanelsTopLeft,
    title: "The agent, the code and the proof, side by side.",
    body: "An agent added a regression test. The editor shows the new lines in the change gutter. The terminal below runs the suite.",
    points: [
      "Split any pane right or down. Drag tabs between groups.",
      "Embedded terminals use libghostty: PTY on Linux, ConPTY on Windows.",
      "Language servers, Find in Files and a Git-aware file tree.",
    ],
    image: "workspace.webp",
    alt: "Crude with an agent summary on the left, repair_test.go open on the right with green change markers, and an embedded terminal showing passing Go tests.",
  },
  {
    value: "agents",
    label: "Agents",
    icon: Bot,
    title: "Delegate the work. Read the report.",
    body: "One agent split a code review across two child reviewers, then merged their reports into one prioritized list with file and line evidence.",
    points: [
      "Child agents get a bounded scope and publish a report when done.",
      "The Board shows every directed message, report and run outcome.",
      "A failed or stopped run stays recoverable. You resume or cancel it.",
    ],
    image: "agents.webp",
    alt: "A merged review report with six prioritized findings above the Board panel, which lists messages and reports between the parent agent and its child reviewers.",
  },
  {
    value: "review",
    label: "Review",
    icon: GitCompareArrows,
    title: "Nothing lands until you stage it.",
    body: "The Git panel shows what the agent changed. Stage or discard each file or hunk, write the message, and commit.",
    points: [
      "Changes, History and Branches in one panel, with Fetch, Pull and Push.",
      "Risky writes and commands stop at an approval prompt with a risk class.",
      "Local History keeps snapshots. /undo previews a restore before it runs.",
    ],
    image: "review.webp",
    alt: "The Git panel under the agent conversation: one modified file, commit controls, and a diff with Stage hunk and Discard hunk actions.",
  },
  {
    value: "skills",
    label: "Skills & MCP",
    icon: Puzzle,
    title: "Give agents new tools, on purpose.",
    body: "Search Agent Skills and MCP servers from built-in sources. Every install shows the exact source, revision and digest first.",
    points: [
      "Sources: Anthropic Skills, the Skills Directory and the official MCP Registry.",
      "MCP over stdio and Streamable HTTP, with endpoint-bound OAuth.",
      "Turn a skill or server off for one session with /skills or /mcps.",
    ],
    image: "skills.webp",
    alt: "The Skills page of Application Settings with search results from Anthropic Skills and the Skills Directory, each with an Install action.",
  },
  {
    value: "palette",
    label: "Commands",
    icon: Command,
    title: "Find any command without a cheat sheet.",
    body: "Ctrl+Alt+P opens the Command Palette. It lists every action with its shortcut. Menus, context menus and drag and drop work with the mouse.",
    points: [
      "Esc always goes back. Ctrl+C always copies.",
      "Alt+F12 opens the terminal. Ctrl+Alt+L splits the pane.",
      "The footer gives one click to Problems, Terminals, Git, Board and Memory.",
    ],
    image: "palette.webp",
    alt: "The Command Palette over the workspace, grouped into Session, File and View commands with keyboard shortcuts.",
  },
  {
    value: "settings",
    label: "Models",
    icon: Settings2,
    title: "Your accounts. Your models. Per role.",
    body: "Connect a provider account, then choose a model for each agent role. Settings apply to the application or to one workspace.",
    points: [
      "Templates for OpenAI, Anthropic, Google, OpenRouter, DeepSeek, Ollama and more.",
      "Any OpenAI-compatible endpoint, including local models.",
      "Memory, command safety and Auto Review policy live on the same page.",
    ],
    image: "settings.webp",
    alt: "Application Settings on the Agents page, listing built-in roles such as Main Agent, Researcher and Crafter with the model each one uses.",
  },
];

const PRINCIPLES = [
  {
    icon: MonitorDown,
    title: "Close the window. The work continues.",
    body: "A per-user core owns workspaces, sessions, agents and terminals. A terminal window is only a shell that attaches to it. Detach, reattach later, or attach over SSH.",
  },
  {
    icon: ShieldCheck,
    title: "Agents show their evidence.",
    body: "Tool rows show the exact command, file and exit code. Reports cite file and line. Risky operations wait for you. Crude is an IDE, not a sandbox, and says so.",
  },
  {
    icon: SquareTerminal,
    title: "Change Crude with Crude.",
    body: "There is no editor plugin system to maintain. Clone the source, open it in Crude, and ask an agent for the change. The repository is documented for agents and people.",
  },
];

const CAPABILITIES: { icon: Icon; title: string; items: string[] }[] = [
  {
    icon: Bot,
    title: "Agents",
    items: [
      "Multi-agent delegation with reports",
      "Plan mode, goals with independent verification, fork",
      "Shared workspace memory",
      "Recoverable runs after provider failures",
      "Managed Git worktrees for isolated runs",
      "Tracked background commands",
    ],
  },
  {
    icon: PanelsTopLeft,
    title: "Editor",
    items: [
      "Syntax highlighting and language servers",
      "Go to Symbol, Find and Replace in Files",
      "Nested split panes and floating windows",
      "Compare with disk or the saved version",
      "Recover editor drafts",
    ],
  },
  {
    icon: SquareTerminal,
    title: "Terminals",
    items: [
      "libghostty terminal engine in the core",
      "Linux PTY and Windows ConPTY",
      "Docked panel or floating window",
      "Terminals keep running when a shell detaches",
    ],
  },
  {
    icon: GitCompareArrows,
    title: "Review and safety",
    items: [
      "Stage or discard by file or hunk",
      "Approval prompts: Manual, Auto Review or YOLO",
      "Local History snapshots and /undo preview",
      "Exact install previews for Skills and MCP",
    ],
  },
  {
    icon: Cpu,
    title: "Models and tools",
    items: [
      "20 provider templates and any OpenAI-compatible API",
      "Local models through Ollama or a custom endpoint",
      "Agent Skills from SKILL.md directories",
      "MCP: stdio, Streamable HTTP and OAuth",
      "Built-in web search and fetch",
    ],
  },
  {
    icon: MonitorDown,
    title: "Platform",
    items: [
      "Linux x86-64 (glibc 2.35+) and Windows x64",
      "One retained core per user",
      "Remote workspaces over SSH",
      "Mouse and keyboard, truecolor, Nerd Font icons",
      "Installers that update in place and roll back",
    ],
  },
];

const FAQS = [
  {
    question: "Is Crude free?",
    answer:
      "Yes. Crude is open source under Apache-2.0. You connect your own model accounts, and your provider can charge you for usage.",
  },
  {
    question: "Which platforms can I use?",
    answer:
      "Linux x86-64 with glibc 2.35 or later, and Windows x64. There are no macOS, ARM64, 32-bit or musl packages at this time.",
  },
  {
    question: "What happens to my agents when I close the terminal?",
    answer:
      "They continue. The window is a shell that detaches from the core. Run crude again in the same project to attach to the same sessions.",
  },
  {
    question: "Does Crude isolate agents from my system?",
    answer:
      "No. Crude is not a security sandbox. It gives you approval prompts, managed worktrees, Git review and Local History. Agent commands run with your user permissions.",
  },
  {
    question: "Why must I accept a risk to install?",
    answer:
      "Crude is beta software that edits files and runs commands. The installer makes that risk explicit. Without your consent, installation stops.",
  },
  {
    question: "Which terminal and font give the best result?",
    answer:
      "Use a current Kitty or Alacritty on Linux, or Windows Terminal on Windows, with truecolor and JetBrainsMono Nerd Font. Then enable Nerd font and file icons in Settings → IDE → Interface.",
  },
];

function asset(name: string) {
  return `${import.meta.env.BASE_URL}assets/${name}`;
}

function GithubMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" className={className}>
      <path d="M12 .7a11.3 11.3 0 0 0-3.6 22c.6.1.8-.3.8-.6v-2.1c-3.2.7-3.9-1.4-3.9-1.4-.5-1.3-1.3-1.7-1.3-1.7-1-.7.1-.7.1-.7 1.2.1 1.8 1.2 1.8 1.2 1 1.8 2.8 1.3 3.5 1 .1-.8.4-1.3.7-1.6-2.6-.3-5.3-1.3-5.3-5.8 0-1.3.5-2.3 1.2-3.2-.1-.3-.5-1.5.1-3.1 0 0 1-.3 3.2 1.2a11 11 0 0 1 5.8 0c2.2-1.5 3.2-1.2 3.2-1.2.6 1.6.2 2.8.1 3.1.8.9 1.2 1.9 1.2 3.2 0 4.5-2.7 5.5-5.3 5.8.4.4.8 1.1.8 2.2v3.3c0 .3.2.7.8.6A11.3 11.3 0 0 0 12 .7Z" />
    </svg>
  );
}

function Logo() {
  return (
    <a href="#top" className="flex items-center gap-2.5 text-foreground" aria-label="Crude home">
      <img src="./favicon.svg" alt="" width={32} height={32} className="size-8" />
      <span className="text-lg font-semibold tracking-tight">Crude</span>
    </a>
  );
}

function fallbackCopy(text: string) {
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.className = "fixed opacity-0";
  document.body.appendChild(area);
  area.select();
  let copied = false;
  try {
    copied = document.execCommand("copy");
  } catch {
    copied = false;
  }
  area.remove();
  return copied;
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | undefined>(undefined);

  useEffect(
    () => () => {
      if (timer.current !== undefined) window.clearTimeout(timer.current);
    },
    [],
  );

  async function copy() {
    let success = false;
    if (navigator.clipboard && window.isSecureContext) {
      try {
        await navigator.clipboard.writeText(text);
        success = true;
      } catch {
        success = fallbackCopy(text);
      }
    } else {
      success = fallbackCopy(text);
    }
    if (!success) return;
    setCopied(true);
    if (timer.current !== undefined) window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setCopied(false), 1600);
  }

  return (
    <Button type="button" variant="ghost" size="sm" onClick={copy} aria-label={label}>
      {copied ? <Check /> : <Copy />}
      <span>{copied ? "Copied" : "Copy"}</span>
    </Button>
  );
}

function Shot({
  image,
  alt,
  priority = false,
}: {
  image: string;
  alt: string;
  priority?: boolean;
}) {
  return (
    <div className="relative">
      <div
        className="pointer-events-none absolute -inset-x-8 -top-8 bottom-0 -z-10 rounded-full bg-primary/10 blur-3xl"
        aria-hidden="true"
      />
      <a
        href={asset(image)}
        className="group block overflow-hidden rounded-xl border border-border bg-card shadow-2xl ring-1 ring-foreground/5 transition-colors hover:border-primary/40"
        aria-label={`${alt} Open the full-size capture.`}
      >
        <img
          src={asset(image)}
          width={SHOT_WIDTH}
          height={SHOT_HEIGHT}
          alt={alt}
          loading={priority ? "eager" : "lazy"}
          decoding="async"
          className="block h-auto w-full"
        />
      </a>
    </div>
  );
}

function Eyebrow({ children }: { children: ReactNode }) {
  return (
    <p className="font-mono text-xs font-medium uppercase tracking-widest text-primary">
      {children}
    </p>
  );
}

function SectionHeading({
  eyebrow,
  title,
  children,
  center = false,
}: {
  eyebrow: string;
  title: string;
  children?: ReactNode;
  center?: boolean;
}) {
  return (
    <div className={center ? "mx-auto max-w-2xl text-center" : "max-w-2xl"}>
      <Eyebrow>{eyebrow}</Eyebrow>
      <h2 className="mt-3 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
        {title}
      </h2>
      {children ? (
        <p className="mt-4 text-pretty text-base leading-7 text-muted-foreground">{children}</p>
      ) : null}
    </div>
  );
}

const NAV = [
  ["Tour", "#tour"],
  ["Why Crude", "#why"],
  ["Capabilities", "#capabilities"],
  ["Install", "#install"],
  ["Docs", DOCS],
];

function SiteHeader() {
  const [menuOpen, setMenuOpen] = useState(false);

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") setMenuOpen(false);
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  return (
    <header className="sticky top-0 z-50 border-b border-border/60 bg-background/75 backdrop-blur-xl">
      <div className="mx-auto flex h-16 w-full max-w-6xl items-center justify-between gap-6 px-4 sm:px-6">
        <Logo />

        <nav className="hidden items-center gap-1 md:flex" aria-label="Primary">
          {NAV.map(([label, href]) => (
            <a
              key={label}
              href={href}
              className="rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:text-foreground"
            >
              {label}
            </a>
          ))}
        </nav>

        <div className="hidden items-center gap-2 md:flex">
          <Button asChild variant="ghost" size="sm">
            <a href={REPOSITORY}>
              <GithubMark className="size-4" />
              GitHub
            </a>
          </Button>
          <Button asChild size="sm">
            <a href="#install">Install</a>
          </Button>
        </div>

        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="md:hidden"
          aria-label={menuOpen ? "Close navigation" : "Open navigation"}
          aria-expanded={menuOpen}
          aria-controls="mobile-navigation"
          onClick={() => setMenuOpen((open) => !open)}
        >
          {menuOpen ? <X /> : <Menu />}
        </Button>
      </div>

      {menuOpen ? (
        <div id="mobile-navigation" className="border-t border-border bg-background px-4 py-3 md:hidden">
          <nav className="flex flex-col" aria-label="Primary mobile">
            {[...NAV, ["GitHub", REPOSITORY]].map(([label, href]) => (
              <a
                key={label}
                href={href}
                className="rounded-md px-3 py-2.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
                onClick={() => setMenuOpen(false)}
              >
                {label}
              </a>
            ))}
          </nav>
        </div>
      ) : null}
    </header>
  );
}

function Hero() {
  return (
    <section className="relative overflow-hidden pt-16 sm:pt-24">
      <div className="pointer-events-none absolute inset-0 bg-grid" aria-hidden="true" />
      <div
        className="pointer-events-none absolute -top-48 left-1/2 size-160 -translate-x-1/2 rounded-full bg-primary/15 blur-3xl"
        aria-hidden="true"
      />

      <div className="relative mx-auto w-full max-w-7xl px-4 sm:px-6">
        <div className="mx-auto max-w-3xl text-center motion-safe:animate-in motion-safe:fade-in motion-safe:slide-in-from-bottom-2 motion-safe:duration-700">
          <Badge variant="outline">
            <span className="size-1.5 rounded-full bg-success" aria-hidden="true" />
            Public beta · Open source · Linux and Windows
          </Badge>

          <h1 className="mt-6 text-balance text-4xl font-semibold tracking-tighter sm:text-6xl lg:text-7xl">
            Build with agents.
            <br />
            <span className="text-muted-foreground">Review everything.</span>
          </h1>

          <p className="mx-auto mt-6 max-w-2xl text-pretty text-lg leading-8 text-muted-foreground">
            Crude is a terminal-native development environment. Your editor,
            terminals, Git review and a team of AI agents share one workspace,
            and the work continues when you close the window.
          </p>

          <div className="mt-8 flex flex-col justify-center gap-3 sm:flex-row">
            <Button asChild size="lg">
              <a href="#install">
                Install Crude
                <ArrowRight />
              </a>
            </Button>
            <Button asChild variant="outline" size="lg">
              <a href="#tour">Take the tour</a>
            </Button>
          </div>

          <p className="mt-5 text-sm text-muted-foreground">
            Apache-2.0 · Bring your own models ·{" "}
            <a href={REPOSITORY} className="inline-flex items-center gap-1 text-foreground hover:text-primary">
              <GithubMark className="size-3.5" />
              opencrude/crude
            </a>
          </p>
        </div>

        <figure className="mx-auto mt-14 max-w-6xl sm:mt-20">
          <Shot
            image="workspace.webp"
            alt="Crude with an agent summary on the left, repair_test.go open on the right with green change markers, and an embedded terminal showing passing Go tests."
            priority
          />
          <figcaption className="mt-4 text-center text-sm text-muted-foreground">
            A real capture. A live agent added a regression test; the terminal confirms it passes.
          </figcaption>
        </figure>
      </div>
    </section>
  );
}

function Facts() {
  const facts = [
    ["Retained core", "Sessions outlive the window"],
    ["Multi-agent", "Delegation with reports"],
    ["20 providers", "Plus any OpenAI-compatible API"],
    ["Skills + MCP", "Installed with exact previews"],
  ];
  return (
    <section aria-label="Key facts" className="py-16 sm:py-20">
      <dl className="mx-auto grid w-full max-w-6xl grid-cols-2 gap-px overflow-hidden rounded-xl border border-border bg-border px-0 lg:grid-cols-4">
        {facts.map(([value, label]) => (
          <div key={value} className="bg-background px-5 py-6 sm:px-6">
            <dt className="text-base font-semibold tracking-tight">{value}</dt>
            <dd className="mt-1 text-sm text-muted-foreground">{label}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

function Tour() {
  return (
    <section id="tour" className="border-t border-border/60 py-24 sm:py-28">
      <div className="mx-auto w-full max-w-6xl px-4 sm:px-6">
        <SectionHeading eyebrow="Tour" title="See the real product." center>
          Each image is a capture of Crude working on its own source code with
          a live model. No mockups and no staged output.
        </SectionHeading>

        <Tabs defaultValue="workspace" className="mt-12 w-full">
          <div className="-mx-4 overflow-x-auto px-4 pb-2 sm:mx-0 sm:px-0">
            <TabsList variant="line" className="mx-auto">
              {TOUR.map(({ value, label, icon: Icon }) => (
                <TabsTrigger key={value} value={value}>
                  <Icon />
                  {label}
                </TabsTrigger>
              ))}
            </TabsList>
          </div>

          {TOUR.map((scene) => (
            <TabsContent key={scene.value} value={scene.value} className="mt-8">
              <div className="grid grid-cols-1 gap-8 lg:grid-cols-2 lg:gap-16">
                <div className="min-w-0">
                  <h3 className="text-balance text-2xl font-semibold tracking-tight">
                    {scene.title}
                  </h3>
                  <p className="mt-3 text-pretty leading-7 text-muted-foreground">{scene.body}</p>
                </div>
                <ul className="min-w-0 space-y-3 lg:pt-1">
                  {scene.points.map((point) => (
                    <li key={point} className="flex gap-3 text-sm leading-6">
                      <Check className="mt-1 size-4 shrink-0 text-primary" />
                      <span className="text-muted-foreground">{point}</span>
                    </li>
                  ))}
                </ul>
              </div>
              <div className="mt-10">
                <Shot image={scene.image} alt={scene.alt} />
              </div>
            </TabsContent>
          ))}
        </Tabs>
      </div>
    </section>
  );
}

function ArchitectureNode({
  title,
  detail,
  accent = false,
}: {
  title: string;
  detail: string;
  accent?: boolean;
}) {
  return (
    <div
      className={
        accent
          ? "rounded-lg border border-primary/40 bg-primary/10 px-4 py-3"
          : "rounded-lg border border-border bg-card px-4 py-3"
      }
    >
      <p className="font-mono text-sm font-medium">{title}</p>
      <p className="mt-0.5 text-xs text-muted-foreground">{detail}</p>
    </div>
  );
}

function Connector({ label }: { label: string }) {
  return (
    <div className="flex items-center justify-center gap-3 py-2" aria-hidden="true">
      <span className="h-6 w-px bg-border" />
      <span className="font-mono text-xs text-muted-foreground">{label}</span>
      <span className="h-6 w-px bg-border" />
    </div>
  );
}

function Why() {
  return (
    <section id="why" className="border-t border-border/60 bg-card/30 py-24 sm:py-28">
      <div className="mx-auto grid w-full max-w-6xl grid-cols-1 gap-14 px-4 sm:px-6 lg:grid-cols-2 lg:items-center">
        <div className="min-w-0">
          <SectionHeading eyebrow="Why Crude" title="Built around the agentic loop, not bolted onto an editor." />
          <div className="mt-10 space-y-8">
            {PRINCIPLES.map(({ icon: Icon, title, body }) => (
              <div key={title} className="flex gap-4">
                <span className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-border bg-background text-primary">
                  <Icon className="size-5" />
                </span>
                <div>
                  <h3 className="font-semibold tracking-tight">{title}</h3>
                  <p className="mt-1.5 text-sm leading-6 text-muted-foreground">{body}</p>
                </div>
              </div>
            ))}
          </div>
        </div>

        <figure className="min-w-0">
          <Card>
            <CardContent>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
                <ArchitectureNode title="shell" detail="Kitty, Alacritty" />
                <ArchitectureNode title="shell" detail="Windows Terminal" />
                <ArchitectureNode title="shell" detail="over SSH" />
              </div>
              <Connector label="typed RPC · attach / detach" />
              <ArchitectureNode
                title="crude core"
                detail="One per user. Owns every workspace actor and keeps it alive."
                accent
              />
              <Connector label="one actor per project" />
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <ArchitectureNode title="~/src/app" detail="sessions · agents · editors" />
                <ArchitectureNode title="~/src/api" detail="terminals · Git · Local History" />
              </div>
            </CardContent>
          </Card>
          <figcaption className="mt-4 text-sm text-muted-foreground">
            Shells are mirrors. The core is the single owner of state, so a
            closed window never stops a run.
          </figcaption>
        </figure>
      </div>
    </section>
  );
}

function Capabilities() {
  return (
    <section id="capabilities" className="border-t border-border/60 py-24 sm:py-28">
      <div className="mx-auto w-full max-w-6xl px-4 sm:px-6">
        <SectionHeading eyebrow="Capabilities" title="Everything in one binary.">
          No extensions to assemble. Each item below ships in the current
          release and has a written contract in the documentation.
        </SectionHeading>

        <div className="mt-12 grid grid-cols-1 gap-px overflow-hidden rounded-xl border border-border bg-border sm:grid-cols-2 lg:grid-cols-3">
          {CAPABILITIES.map(({ icon: Icon, title, items }) => (
            <div key={title} className="bg-background p-6">
              <div className="flex items-center gap-2.5">
                <Icon className="size-4 text-primary" />
                <h3 className="font-semibold tracking-tight">{title}</h3>
              </div>
              <ul className="mt-4 space-y-2">
                {items.map((item) => (
                  <li key={item} className="text-sm leading-6 text-muted-foreground">
                    {item}
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>

        <a
          href={DOCS}
          className="mt-8 inline-flex items-center gap-1.5 text-sm font-medium text-brand hover:text-foreground"
        >
          Read the contracts in the documentation
          <ArrowUpRight className="size-4" />
        </a>
      </div>
    </section>
  );
}

function CommandBlock({ shell, command, label }: { shell: string; command: string; label: string }) {
  return (
    <div className="overflow-hidden rounded-lg border border-border bg-background">
      <div className="flex items-center justify-between border-b border-border px-4 py-1.5">
        <span className="font-mono text-xs text-muted-foreground">{shell}</span>
        <CopyButton text={command} label={label} />
      </div>
      <pre className="overflow-x-auto p-4 font-mono text-xs leading-6">
        <code>
          {command.split("\n").map((line) => (
            <span key={line} className={line.startsWith("#") ? "block text-muted-foreground" : "block text-foreground"}>
              {line}
            </span>
          ))}
        </code>
      </pre>
    </div>
  );
}

function Install() {
  return (
    <section id="install" className="border-t border-border/60 bg-card/30 py-24 sm:py-28">
      <div className="mx-auto grid w-full max-w-6xl grid-cols-1 gap-12 px-4 sm:px-6 lg:grid-cols-12 lg:items-start">
        <div className="min-w-0 lg:col-span-4">
          <SectionHeading eyebrow="Install" title="One command. No compiler.">
            The installer downloads a verified release. Run the same command
            again to update. You do not need administrator rights.
          </SectionHeading>

          <ol className="mt-8 space-y-4 text-sm leading-6">
            {[
              ["Install", "Run the command for your platform."],
              ["Open a project", "Run crude in the project folder."],
              ["Connect a model", "Go to Tools → Settings → Accounts, then select a model under Agents."],
            ].map(([title, body], index) => (
              <li key={title} className="flex gap-4">
                <span className="flex size-6 shrink-0 items-center justify-center rounded-full border border-border font-mono text-xs text-muted-foreground">
                  {index + 1}
                </span>
                <p className="text-muted-foreground">
                  <strong className="font-medium text-foreground">{title}.</strong> {body}
                </p>
              </li>
            ))}
          </ol>
        </div>

        <div className="min-w-0 lg:col-span-8">
          <Card>
            <CardContent>
              <Tabs defaultValue="linux" className="w-full">
                <TabsList className="w-full">
                  <TabsTrigger value="linux">Linux x86-64</TabsTrigger>
                  <TabsTrigger value="windows">Windows x64</TabsTrigger>
                </TabsList>

                <TabsContent value="linux" className="mt-4">
                  <CommandBlock shell="bash" command={LINUX_COMMAND} label="Copy Linux install commands" />
                  <p className="mt-3 text-xs leading-5 text-muted-foreground">
                    Installs to ~/.local/bin and adds one PATH line. Use --no-path
                    for managed shells. Needs glibc 2.35 or later.
                  </p>
                </TabsContent>

                <TabsContent value="windows" className="mt-4">
                  <CommandBlock shell="PowerShell" command={WINDOWS_COMMAND} label="Copy Windows install commands" />
                  <p className="mt-3 text-xs leading-5 text-muted-foreground">
                    Installs to %LOCALAPPDATA%\Crude\bin. Keeps your PATH and rolls
                    back a failed update. Needs PowerShell 5.1 or later.
                  </p>
                </TabsContent>
              </Tabs>

              <div className="mt-6 flex gap-3 rounded-lg border border-warning/30 bg-warning/10 p-4 text-xs leading-5 text-muted-foreground">
                <ShieldCheck className="mt-0.5 size-4 shrink-0 text-warning" />
                <p>
                  <strong className="font-medium text-warning">Beta software.</strong>{" "}
                  Crude and its agents can change files and run commands. A bug or
                  an agent mistake can damage or delete work. Commit often, keep
                  backups and review agent actions.
                </p>
              </div>
            </CardContent>
          </Card>

          <p className="mt-4 text-sm text-muted-foreground">
            Prefer source?{" "}
            <a href={BUILD} className="text-brand hover:text-foreground">
              Build Crude from source
            </a>{" "}
            · <a href={RELEASES} className="text-brand hover:text-foreground">All releases</a>
          </p>
        </div>
      </div>
    </section>
  );
}

function Questions() {
  return (
    <section id="faq" className="border-t border-border/60 py-24 sm:py-28">
      <div className="mx-auto grid w-full max-w-6xl grid-cols-1 gap-12 px-4 sm:px-6 lg:grid-cols-12">
        <div className="lg:col-span-4">
          <SectionHeading eyebrow="Questions" title="Straight answers." />
          <p className="mt-4 text-sm leading-6 text-muted-foreground">
            Something missing?{" "}
            <a href={ISSUES} className="text-brand hover:text-foreground">
              Open an issue
            </a>
            .
          </p>
        </div>
        <Accordion type="single" collapsible className="w-full lg:col-span-8">
          {FAQS.map(({ question, answer }, index) => (
            <AccordionItem key={question} value={`faq-${index}`}>
              <AccordionTrigger>{question}</AccordionTrigger>
              <AccordionContent>
                <p className="max-w-2xl leading-7 text-muted-foreground">{answer}</p>
              </AccordionContent>
            </AccordionItem>
          ))}
        </Accordion>
      </div>
    </section>
  );
}

function ClosingCall() {
  return (
    <section className="relative overflow-hidden border-t border-border/60 py-24 sm:py-28">
      <div
        className="pointer-events-none absolute -bottom-40 left-1/2 size-160 -translate-x-1/2 rounded-full bg-primary/10 blur-3xl"
        aria-hidden="true"
      />
      <div className="relative mx-auto flex w-full max-w-2xl flex-col items-center px-4 text-center sm:px-6">
        <img src="./favicon.svg" alt="" width={64} height={64} className="size-16" />
        <h2 className="mt-6 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
          Give your agents a real workspace.
        </h2>
        <p className="mt-4 text-pretty text-muted-foreground">
          Install Crude in one command, connect a model, and start your first
          session in the project you already have.
        </p>
        <div className="mt-8 flex flex-col gap-3 sm:flex-row">
          <Button asChild size="lg">
            <a href="#install">
              Install Crude
              <ArrowRight />
            </a>
          </Button>
          <Button asChild variant="outline" size="lg">
            <a href={REPOSITORY}>
              <GithubMark className="size-4" />
              Star on GitHub
            </a>
          </Button>
        </div>
      </div>
    </section>
  );
}

function SiteFooter() {
  const columns: [string, [string, string][]][] = [
    ["Product", [["Tour", "#tour"], ["Capabilities", "#capabilities"], ["Install", "#install"], ["Releases", RELEASES]]],
    ["Resources", [["Documentation", DOCS], ["Agent guide", AGENTS], ["Build from source", BUILD], ["Report a bug", ISSUES]]],
    ["Project", [["Source code", REPOSITORY], ["Apache-2.0", LICENSE], ["Notices", NOTICE]]],
  ];

  return (
    <footer className="border-t border-border">
      <div className="mx-auto grid w-full max-w-6xl grid-cols-2 gap-10 px-4 py-14 sm:px-6 lg:grid-cols-5">
        <div className="col-span-2">
          <Logo />
          <p className="mt-4 max-w-xs text-sm leading-6 text-muted-foreground">
            The terminal-native development environment for people and their agents.
          </p>
        </div>
        {columns.map(([title, links]) => (
          <div key={title}>
            <h3 className="text-sm font-medium">{title}</h3>
            <ul className="mt-4 space-y-2.5">
              {links.map(([label, href]) => (
                <li key={label}>
                  <a href={href} className="text-sm text-muted-foreground hover:text-foreground">
                    {label}
                  </a>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
      <Separator />
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-2 px-4 py-6 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between sm:px-6">
        <p>© {new Date().getFullYear()} Crude contributors. Apache-2.0.</p>
        <p className="flex items-center gap-2">
          <span className="size-1.5 rounded-full bg-success" aria-hidden="true" />
          Public beta. Active development.
        </p>
      </div>
    </footer>
  );
}

export default function App() {
  return (
    <div id="top" className="min-h-screen bg-background text-foreground">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-50 focus:rounded-md focus:bg-primary focus:px-4 focus:py-2 focus:font-medium focus:text-primary-foreground"
      >
        Skip to content
      </a>
      <SiteHeader />
      <main id="main">
        <Hero />
        <div className="px-4 sm:px-6">
          <Facts />
        </div>
        <Tour />
        <Why />
        <Capabilities />
        <Install />
        <Questions />
        <ClosingCall />
      </main>
      <SiteFooter />
    </div>
  );
}
