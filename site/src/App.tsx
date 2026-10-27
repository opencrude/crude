import { useEffect, useRef, useState, type ReactNode } from "react";
import {
  ArrowRight,
  Check,
  Copy,
  Cpu,
  Download,
  GitCompareArrows,
  Laptop,
  Menu,
  Network,
  PanelsTopLeft,
  ShieldCheck,
  Terminal,
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
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
} from "@/components/ui/card";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

const REPOSITORY = "https://github.com/opencrude/crude";
const RELEASES = `${REPOSITORY}/releases`;
const DOCS = `${REPOSITORY}/blob/master/docs/README.md`;
const AGENT_DOCS = `${REPOSITORY}/blob/master/docs/reference/agent-orchestration.md`;
const RELEASE_GUIDE = `${REPOSITORY}/blob/master/docs/guides/releases.md`;
const VALIDATION_GUIDE = `${REPOSITORY}/blob/master/docs/guides/validation.md`;
const LICENSE = `${REPOSITORY}/blob/master/LICENSE-APACHE`;
const NOTICE = `${REPOSITORY}/blob/master/NOTICE`;
const AGENTS = `${REPOSITORY}/blob/master/AGENTS.md`;
const ISSUES = `${REPOSITORY}/issues`;

const LINUX_COMMAND = `# review the installer, then run it
curl -fsSL ${RELEASES}/latest/download/install.sh -o install.sh
bash install.sh --accept-beta-risk`;

const WINDOWS_COMMAND = `# review the installer, then run it
Invoke-WebRequest ${RELEASES}/latest/download/install.ps1 -OutFile install.ps1
.\\install.ps1 -AcceptBetaRisk`;

const FEATURES = [
  {
    icon: Terminal,
    title: "Terminal-native",
    description:
      "A real TUI with native Linux PTY and Windows ConPTY terminals. Mouse and keyboard, truecolor, and a layout that stays readable at constrained sizes.",
  },
  {
    icon: PanelsTopLeft,
    title: "An editor you can live in",
    description:
      "Syntax highlighting, file search, language servers, split panes and floating windows. Open what you need without leaving the workspace.",
  },
  {
    icon: Network,
    title: "Agents with a workspace",
    description:
      "Multi-agent delegation, plans, reports, shared project memory and recoverable interrupted work. Closing a shell detaches it; it does not deliberately kill running work.",
  },
  {
    icon: GitCompareArrows,
    title: "Review before trust",
    description:
      "Git diffs, staging, history, permission controls, managed worktrees and Local History previews and undo. Crude is an IDE, not a security sandbox.",
  },
  {
    icon: Cpu,
    title: "Your models and tools",
    description:
      "Configurable provider accounts, local models, Skills and MCP integrations. Bring your own account; provider charges may apply.",
  },
  {
    icon: ShieldCheck,
    title: "Command-only automation",
    description:
      "No automatic CI on push, pull request or tag. Verification, releases and website deployment run only when you deliberately dispatch them.",
  },
];

const AGENT_CHECKS = [
  {
    title: "Delegation that stays bounded.",
    body: "Children own a clear scope, report back, and keep file ownership explicit.",
  },
  {
    title: "Work you can inspect.",
    body: "Read exact reports, tool activity and terminal output instead of guessing from a spinner.",
  },
  {
    title: "Recovery by default.",
    body: "Provider failures stop an attempt, not the work. Resume or cancel with the context intact.",
  },
  {
    title: "Shared project memory.",
    body: "Durable conventions stay available across sessions and agents.",
  },
];

const EDITORIAL = [
  {
    index: "01",
    title: "See the diff",
    body: "Review changes file by file, stage what belongs, and keep unrelated work out of the commit.",
  },
  {
    index: "02",
    title: "Undo with a receipt",
    body: "Local History snapshots are previewed first, then restored with precondition checks and an undo path.",
  },
  {
    index: "03",
    title: "Keep the core alive",
    body: "Shells attach to retained workspace actors. Close the window; your sessions and agent work remain owned by the core.",
  },
  {
    index: "04",
    title: "Stay honest at the edge",
    body: "Missing evidence is reported as missing. Stale asynchronous results are dropped, not painted as current state.",
  },
];

const FAQS = [
  {
    question: "Is Crude free and open source?",
    answer:
      "Yes. Crude's original code is Apache-2.0. Third-party code keeps its own license, and releases bundle the required notices. You bring your own model accounts; provider charges may apply.",
  },
  {
    question: "Which platforms are supported?",
    answer:
      "Linux x86-64 and Windows x64. The embedded terminal uses a pinned native Ghostty library and cgo. There are no macOS, ARM64, 32-bit or Alpine/musl release packages advertised.",
  },
  {
    question: "Does closing the terminal stop my agents?",
    answer:
      "No. Closing a shell detaches it from the retained core; it does not deliberately terminate running agent work. Finish the work and stop the old core yourself before switching binaries if a protocol mismatch is reported.",
  },
  {
    question: "Can I use local models or my own provider?",
    answer:
      "Yes. Add provider accounts in Tools → Settings → Accounts, then select the model under Agents. Local models and MCP/Skills integrations are supported when configured.",
  },
  {
    question: "Why does the installer ask me to accept a risk?",
    answer:
      "Crude is beta software that can edit files and run commands. The warning is there to make the trade-off explicit. Installation and updates fail closed without consent when no interactive terminal is available.",
  },
];

function asset(name: string) {
  return `${import.meta.env.BASE_URL}assets/${name}`;
}

function GithubMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
      className={className}
    >
      <path d="M12 .7a11.3 11.3 0 0 0-3.6 22c.6.1.8-.3.8-.6v-2.1c-3.2.7-3.9-1.4-3.9-1.4-.5-1.3-1.3-1.7-1.3-1.7-1-.7.1-.7.1-.7 1.2.1 1.8 1.2 1.8 1.2 1 1.8 2.8 1.3 3.5 1 .1-.8.4-1.3.7-1.6-2.6-.3-5.3-1.3-5.3-5.8 0-1.3.5-2.3 1.2-3.2-.1-.3-.5-1.5.1-3.1 0 0 1-.3 3.2 1.2a11 11 0 0 1 5.8 0c2.2-1.5 3.2-1.2 3.2-1.2.6 1.6.2 2.8.1 3.1.8.9 1.2 1.9 1.2 3.2 0 4.5-2.7 5.5-5.3 5.8.4.4.8 1.1.8 2.2v3.3c0 .3.2.7.8.6A11.3 11.3 0 0 0 12 .7Z" />
    </svg>
  );
}

function fallbackCopy(text: string) {
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.opacity = "0";
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
    <Button
      type="button"
      variant="outline"
      size="sm"
      onClick={copy}
      aria-label={label}
    >
      {copied ? <Check /> : <Copy />}
      <span>{copied ? "Copied" : "Copy"}</span>
    </Button>
  );
}

function WindowFrame({
  title,
  image,
  alt,
  caption,
  priority = false,
}: {
  title: string;
  image: string;
  alt: string;
  caption: string;
  priority?: boolean;
}) {
  return (
    <figure className="relative mx-auto w-full min-w-0 max-w-2xl lg:max-w-none">
      <div
        className="pointer-events-none absolute -inset-6 -z-10 rounded-full bg-primary/10 blur-3xl"
        aria-hidden="true"
      />
      <div className="overflow-hidden rounded-xl border border-border bg-card shadow-2xl">
        <div className="flex items-center gap-2 border-b border-border bg-secondary/60 px-4 py-2.5">
          <span className="size-2.5 rounded-full bg-destructive" aria-hidden="true" />
          <span className="size-2.5 rounded-full bg-warning" aria-hidden="true" />
          <span className="size-2.5 rounded-full bg-success" aria-hidden="true" />
          <span className="ml-2 font-mono text-xs text-muted-foreground">
            {title}
          </span>
        </div>
        <img
          src={image}
          width={1440}
          height={966}
          alt={alt}
          loading={priority ? "eager" : "lazy"}
          className="block w-full"
        />
      </div>
      <figcaption className="mt-3 text-right text-xs text-muted-foreground max-md:text-left">
        {caption}
      </figcaption>
    </figure>
  );
}

function SectionHeading({
  eyebrow,
  title,
  children,
}: {
  eyebrow: string;
  title: string;
  children: ReactNode;
}) {
  return (
    <div className="max-w-3xl">
      <p className="mb-4 flex items-center gap-2 font-mono text-xs font-semibold uppercase tracking-widest text-primary">
        <span className="size-1.5 rounded-full bg-primary" aria-hidden="true" />
        {eyebrow}
      </p>
      <h2 className="text-balance text-3xl font-semibold tracking-tight sm:text-4xl lg:text-5xl">
        {title}
      </h2>
      <p className="mt-4 text-pretty text-base leading-7 text-muted-foreground sm:text-lg">
        {children}
      </p>
    </div>
  );
}

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
    <header className="sticky top-0 z-50 border-b border-border/70 bg-background/80 backdrop-blur-xl">
      <div className="mx-auto flex h-16 w-full max-w-7xl items-center justify-between px-6 lg:px-8">
        <a
          href="#top"
          className="flex items-center gap-2.5 text-foreground"
          aria-label="Crude home"
        >
          <span className="flex size-9 items-center justify-center rounded-lg border border-primary/40 bg-primary/10 font-mono text-sm font-bold text-primary">
            &gt;_
          </span>
          <span className="font-mono text-sm font-bold tracking-widest">
            CRUDE
          </span>
        </a>

        <div className="hidden items-center gap-1 md:flex">
          <nav className="flex items-center gap-1" aria-label="Primary">
            <a
              href="#product"
              className="rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              Product
            </a>
            <a
              href="#agents"
              className="rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              Agents
            </a>
            <a
              href="#install"
              className="rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              Install
            </a>
            <a
              href={DOCS}
              className="rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              Docs
            </a>
          </nav>
          <Button asChild variant="outline" size="sm">
            <a href={REPOSITORY}>
              <GithubMark className="size-4" />
              GitHub
            </a>
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
        <div
          id="mobile-navigation"
          className="border-t border-border bg-background px-6 py-4 md:hidden"
        >
          <nav className="flex flex-col gap-1" aria-label="Primary mobile">
            {[
              ["Product", "#product"],
              ["Agents", "#agents"],
              ["Install", "#install"],
              ["Docs", DOCS],
            ].map(([label, href]) => (
              <a
                key={label}
                href={href}
                className="rounded-md px-3 py-2.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
                onClick={() => setMenuOpen(false)}
              >
                {label}
              </a>
            ))}
            <a
              href={REPOSITORY}
              className="mt-2 flex items-center gap-2 rounded-md px-3 py-2.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
              onClick={() => setMenuOpen(false)}
            >
              <GithubMark className="size-4" />
              GitHub
            </a>
          </nav>
        </div>
      ) : null}
    </header>
  );
}

function Hero() {
  return (
    <section className="relative overflow-hidden pb-24 pt-16 sm:pb-32 sm:pt-24">
      <div className="pointer-events-none absolute inset-0 bg-grid" aria-hidden="true" />
      <div
        className="pointer-events-none absolute -top-40 right-0 size-96 rounded-full bg-primary/10 blur-3xl"
        aria-hidden="true"
      />
      <div
        className="pointer-events-none absolute inset-x-0 top-0 h-px bg-linear-to-r from-transparent via-primary/60 to-transparent"
        aria-hidden="true"
      />

      <div className="relative mx-auto grid w-full max-w-7xl grid-cols-1 gap-16 px-6 lg:grid-cols-2 lg:items-center lg:px-8">
        <div className="min-w-0 max-w-2xl">
          <Badge variant="outline" className="mb-6">
            <span className="size-1.5 rounded-full bg-primary" aria-hidden="true" />
            Agentic Development Environment
          </Badge>

          <h1 className="text-balance text-5xl font-semibold leading-tight tracking-tight sm:text-6xl lg:text-7xl">
            Your editor, terminal and agents.{" "}
            <span className="text-primary">One workspace.</span>
          </h1>

          <p className="mt-6 max-w-2xl text-pretty text-lg leading-8 text-muted-foreground">
            Crude is an opinionated, open-source IDE built around agentic
            development — not an agent bolted onto an editor. Humans and agents
            get first-class tools, one coherent workspace and no plugin
            treadmill.
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
                View source
              </a>
            </Button>
          </div>

          <Card className="mt-8 min-w-0 max-w-2xl">
            <CardContent>
              <div className="flex items-center gap-3">
                <code className="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground sm:text-sm">
                  {LINUX_COMMAND.split("\n").slice(1).join(" && ")}
                </code>
                <CopyButton
                  text={LINUX_COMMAND}
                  label="Copy Linux install commands"
                />
              </div>
            </CardContent>
          </Card>

          <p className="mt-3 text-xs text-muted-foreground">
            Linux x86-64 ·{" "}
            <a href="#install" className="text-brand hover:text-foreground">
              Windows installer
            </a>{" "}
            · Requires explicit beta-risk consent
          </p>

          <ul className="mt-6 flex flex-wrap gap-x-5 gap-y-2 font-mono text-xs uppercase tracking-widest text-muted-foreground">
            {["Linux + Windows", "Apache-2.0", "Bring your own models"].map(
              (item) => (
                <li key={item} className="flex items-center gap-2">
                  <span className="size-1 rounded-full bg-border" aria-hidden="true" />
                  {item}
                </li>
              ),
            )}
          </ul>
        </div>

        <WindowFrame
          title="crude — ~/src/crude"
          image={asset("crude-source.webp")}
          alt="Crude editing its own Go source with syntax highlighting, the file tree, an agent conversation and an embedded terminal."
          caption="Crude editing its own source. Real product capture, not a mockup."
          priority
        />
      </div>
    </section>
  );
}

function Product() {
  return (
    <section id="product" className="border-t border-border/70 py-24 sm:py-32">
      <div className="mx-auto w-full max-w-7xl px-6 lg:px-8">
        <SectionHeading
          eyebrow="Less configuring. More developing."
          title="An IDE that keeps humans and agents in the same room."
        >
          Useful defaults, discoverable controls and one workspace for the whole
          job. Click, drag, browse menus or take a shortcut when it helps.
        </SectionHeading>

        <div className="mt-12 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {FEATURES.map(({ icon: Icon, title, description }) => (
            <Card key={title} className="h-full">
              <CardHeader>
                <div className="mb-2 flex size-10 items-center justify-center rounded-lg border border-primary/25 bg-primary/10 text-primary">
                  <Icon className="size-5" />
                </div>
                <h3 className="text-lg font-semibold tracking-tight">{title}</h3>
                <CardDescription>{description}</CardDescription>
              </CardHeader>
            </Card>
          ))}
        </div>
      </div>
    </section>
  );
}

function Agents() {
  return (
    <section
      id="agents"
      className="border-y border-border/70 bg-card/30 py-24 sm:py-32"
    >
      <div className="mx-auto grid w-full max-w-7xl grid-cols-1 gap-16 px-6 lg:grid-cols-2 lg:items-center lg:px-8">
        <div className="min-w-0">
          <SectionHeading
            eyebrow="The agentic loop is sacred"
            title="Delegate the work. Keep the context."
          >
            Crude treats agents as teammates with durable identities, not chat
            tabs. Give one a bounded task, review its report, and keep working
            while it runs. Interrupted runs stay recoverable instead of
            disappearing.
          </SectionHeading>

          <ul className="mt-8 space-y-4">
            {AGENT_CHECKS.map(({ title, body }) => (
              <li key={title} className="flex gap-3">
                <span className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full bg-success/10 text-success">
                  <Check className="size-3" />
                </span>
                <p className="text-sm leading-6 text-muted-foreground">
                  <strong className="font-medium text-foreground">{title}</strong>{" "}
                  {body}
                </p>
              </li>
            ))}
          </ul>

          <a
            href={AGENT_DOCS}
            className="mt-8 inline-flex items-center gap-1.5 text-sm font-medium text-brand hover:text-foreground"
          >
            Read the agent orchestration contract
            <ArrowRight className="size-4" />
          </a>
        </div>

        <WindowFrame
          title="agents alongside the code"
          image={asset("crude-agents.webp")}
          alt="Crude's agent conversation beside the Crude source tree, with tool activity and terminal output."
          caption="Real product capture. The example conversation is a local fixture, not a claim about a live model's output."
        />
      </div>
    </section>
  );
}

function Editorial() {
  return (
    <section className="py-24 sm:py-32">
      <div className="mx-auto grid w-full max-w-7xl grid-cols-1 gap-12 px-6 lg:grid-cols-5 lg:items-start lg:px-8">
        <div className="min-w-0 lg:col-span-2">
          <SectionHeading
            eyebrow="Built for real work"
            title="Trust comes from evidence, not vibes."
          >
            Agents can change files and run commands. Crude gives you the
            surfaces to see what happened, compare it, and undo it before it
            becomes someone else's problem.
          </SectionHeading>
        </div>

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:col-span-3">
          {EDITORIAL.map(({ index, title, body }) => (
            <Card key={title} className="h-full">
              <CardHeader>
                <span className="font-mono text-xs font-semibold tracking-widest text-primary">
                  {index}
                </span>
                <h3 className="text-base font-semibold tracking-tight">{title}</h3>
                <CardDescription>{body}</CardDescription>
              </CardHeader>
            </Card>
          ))}
        </div>
      </div>
    </section>
  );
}

function Install() {
  return (
    <section
      id="install"
      className="border-t border-border/70 bg-linear-to-b from-primary/5 via-background to-background py-24 sm:py-32"
    >
      <div className="mx-auto grid w-full max-w-7xl gap-12 px-6 lg:grid-cols-5 lg:items-start lg:px-8">
        <div className="lg:col-span-2">
          <SectionHeading
            eyebrow="Install or update"
            title="One command. No compiler."
          >
            Download the installer from a published release, review it, then run
            it. Rerun the same command to update. No source checkout or elevation
            is required.
          </SectionHeading>

          <p className="mt-6 font-mono text-xs leading-6 text-muted-foreground">
            Linux x86-64 with glibc 2.35+ · Windows x64 with ConPTY · PowerShell
            5.1+
          </p>

          <a
            href={RELEASES}
            className="mt-6 inline-flex items-center gap-1.5 text-sm font-medium text-brand hover:text-foreground"
          >
            Browse all releases
            <ArrowRight className="size-4" />
          </a>
        </div>

        <Card className="min-w-0 overflow-hidden lg:col-span-3">
          <CardContent>
            <Tabs defaultValue="linux" className="w-full">
              <TabsList className="grid w-full grid-cols-2">
                <TabsTrigger value="linux">
                  <Laptop />
                  Linux
                </TabsTrigger>
                <TabsTrigger value="windows">
                  <Download />
                  Windows
                </TabsTrigger>
              </TabsList>

              <TabsContent value="linux">
                <div className="space-y-4">
                  <div className="overflow-hidden rounded-lg border border-border bg-background">
                    <div className="flex items-center justify-between border-b border-border bg-secondary/40 px-4 py-2">
                      <span className="font-mono text-xs text-muted-foreground">
                        bash
                      </span>
                      <CopyButton
                        text={LINUX_COMMAND}
                        label="Copy Linux install commands"
                      />
                    </div>
                    <pre className="overflow-x-auto p-4 font-mono text-xs leading-6 text-muted-foreground">
                      <code>{LINUX_COMMAND}</code>
                    </pre>
                  </div>
                  <p className="text-xs leading-6 text-muted-foreground">
                    Installs under ~/.local/bin and adds one idempotent PATH line.
                    Use --no-path for managed shells.
                  </p>
                </div>
              </TabsContent>

              <TabsContent value="windows">
                <div className="space-y-4">
                  <div className="overflow-hidden rounded-lg border border-border bg-background">
                    <div className="flex items-center justify-between border-b border-border bg-secondary/40 px-4 py-2">
                      <span className="font-mono text-xs text-muted-foreground">
                        PowerShell
                      </span>
                      <CopyButton
                        text={WINDOWS_COMMAND}
                        label="Copy Windows install commands"
                      />
                    </div>
                    <pre className="overflow-x-auto p-4 font-mono text-xs leading-6 text-muted-foreground">
                      <code>{WINDOWS_COMMAND}</code>
                    </pre>
                  </div>
                  <p className="text-xs leading-6 text-muted-foreground">
                    Installs under %LOCALAPPDATA%\Crude\bin, preserves your PATH
                    and rolls back failed updates.
                  </p>
                </div>
              </TabsContent>
            </Tabs>

            <div className="mt-6 rounded-lg border border-warning/30 bg-warning/10 p-4 text-xs leading-6 text-muted-foreground">
              <strong className="font-medium text-warning">
                Active development / beta.
              </strong>{" "}
              Crude and its agents can change files and execute commands. Bugs or
              agent mistakes can damage or lose work. Commit your changes, keep
              independent backups and review agent actions. Installing requires
              explicit acceptance.
            </div>
          </CardContent>
        </Card>
      </div>
    </section>
  );
}

function Questions() {
  return (
    <section className="py-24 sm:py-32">
      <div className="mx-auto w-full max-w-3xl px-6 lg:px-8">
        <SectionHeading eyebrow="Questions" title="Before you dive in.">
          The practical details, without the marketing fog.
        </SectionHeading>

        <Accordion type="single" collapsible className="mt-8 w-full">
          {FAQS.map(({ question, answer }, index) => (
            <AccordionItem key={question} value={`faq-${index}`}>
              <AccordionTrigger>{question}</AccordionTrigger>
              <AccordionContent>
                <p className="max-w-2xl leading-7 text-muted-foreground">
                  {answer}
                </p>
              </AccordionContent>
            </AccordionItem>
          ))}
        </Accordion>
      </div>
    </section>
  );
}

function SiteFooter() {
  const columns = [
    {
      title: "Product",
      links: [
        ["Features", "#product"],
        ["Agents", "#agents"],
        ["Install", "#install"],
        ["Releases", RELEASES],
      ],
    },
    {
      title: "Resources",
      links: [
        ["Documentation", DOCS],
        ["Release guide", RELEASE_GUIDE],
        ["Validation", VALIDATION_GUIDE],
        ["Report a bug", ISSUES],
      ],
    },
    {
      title: "Project",
      links: [
        ["Source code", REPOSITORY],
        ["Apache-2.0", LICENSE],
        ["Notices", NOTICE],
        ["Agent guide", AGENTS],
      ],
    },
  ];

  return (
    <footer className="border-t border-border bg-background">
      <div className="mx-auto grid w-full max-w-7xl grid-cols-1 gap-10 px-6 py-14 sm:grid-cols-2 lg:grid-cols-4 lg:px-8">
        <div className="sm:col-span-2 lg:col-span-1">
          <a
            href="#top"
            className="flex items-center gap-2.5 text-foreground"
            aria-label="Crude home"
          >
            <span className="flex size-9 items-center justify-center rounded-lg border border-primary/40 bg-primary/10 font-mono text-sm font-bold text-primary">
              &gt;_
            </span>
            <span className="font-mono text-sm font-bold tracking-widest">
              CRUDE
            </span>
          </a>
          <p className="mt-4 max-w-xs text-sm leading-6 text-muted-foreground">
            The hackable Agentic Development Environment. Built for humans and
            agents. Neither in the other&apos;s way.
          </p>
        </div>

        {columns.map(({ title, links }) => (
          <div key={title}>
            <h3 className="font-mono text-xs font-semibold uppercase tracking-widest text-foreground">
              {title}
            </h3>
            <ul className="mt-4 space-y-2.5">
              {links.map(([label, href]) => (
                <li key={label}>
                  <a
                    href={href}
                    className="text-sm text-muted-foreground hover:text-foreground"
                  >
                    {label}
                  </a>
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>

      <div className="mx-auto flex w-full max-w-7xl flex-col gap-3 border-t border-border px-6 py-6 text-xs text-muted-foreground sm:flex-row sm:items-center sm:justify-between lg:px-8">
        <p>© {new Date().getFullYear()} Crude contributors. Apache-2.0.</p>
        <p className="flex items-center gap-2 font-mono uppercase tracking-widest">
          <span className="size-1.5 rounded-full bg-success" aria-hidden="true" />
          Active development · beta
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
        <Product />
        <Agents />
        <Editorial />
        <Install />
        <Questions />
      </main>
      <SiteFooter />
    </div>
  );
}
