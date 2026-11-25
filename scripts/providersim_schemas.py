#!/usr/bin/env python3
"""Build the simulator's offline JSON Schema bundles from immutable upstreams.

Install scripts/providersim_requirements.txt, then run this script. Use --check
in a maintainer check to compare every generated file without changing it.
Normal Go builds and tests need neither Python nor network access.
"""

# SDK imports resolve in the generator venv from scripts/providersim_requirements.txt.
# pyright: reportMissingImports=false

from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
import re
import tarfile
import tempfile
import urllib.request
import zipfile
from dataclasses import dataclass
from importlib.metadata import PackageNotFoundError, distribution
from importlib.resources import files as package_files
from pathlib import Path
from typing import TYPE_CHECKING, Any, Callable
from urllib.parse import unquote

import yaml

if TYPE_CHECKING:
    from pydantic.json_schema import JsonSchemaMode

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "internal" / "providersim" / "schemas"
VERSION = 4
DIALECT = "https://json-schema.org/draft/2020-12/schema"
OAS_COMPONENTS = "#/components/schemas/"
DEFS = "#/$defs/"
DRAFT7_DEFS = "#/definitions/"


@dataclass(frozen=True)
class Source:
    repo: str
    revision: str
    path: str
    sha256: str
    license: str
    definitions: Callable[[bytes], dict]
    license_sha256: str | None = None

    def url(self, path):
        return f"https://raw.githubusercontent.com/{self.repo}/{self.revision}/{path}"


def openapi(loader):
    def definitions(raw):
        document = loader(raw)
        if not document["openapi"].startswith("3.1."):
            raise ValueError("expected OpenAPI 3.1")
        if document.get("jsonSchemaDialect") not in (None, DIALECT, "https://spec.openapis.org/oas/3.1/dialect/base"):
            raise ValueError("unsupported OpenAPI schema dialect")
        return document["components"]["schemas"]

    return definitions


def draft7_definitions(raw):
    doc = json.loads(raw)
    if doc["$schema"] != "http://json-schema.org/draft-07/schema#":
        raise ValueError("expected draft-07")
    # This pinned Schemars extract uses only keywords with the same semantics
    # in both drafts; no tuple arrays, nested resources or ref siblings.
    unchanged = {"$ref", "type", "description", "title", "format", "enum", "default", "properties",
                 "required", "oneOf", "anyOf", "additionalProperties", "items", "minLength", "minimum"}

    def check(schema):
        if isinstance(schema, bool):
            return
        if schema.keys() - unchanged or ("$ref" in schema and len(schema) != 1):
            raise ValueError("draft-07 migration requires equivalent keywords and standalone refs")
        if "$ref" in schema and not schema["$ref"].startswith(DRAFT7_DEFS):
            raise ValueError("expected a local draft-07 definition reference")
        for child in schema.get("properties", {}).values():
            check(child)
        for key in ("anyOf", "oneOf"):
            for child in schema.get(key, []):
                check(child)
        for key in ("items", "additionalProperties"):
            if key in schema:
                if not isinstance(schema[key], (bool, dict)):
                    raise ValueError(f"cannot migrate draft-07 {key} value")
                check(schema[key])

    for definition in doc["definitions"].values():
        check(definition)
    return doc["definitions"]


SOURCES = {
    "codex": Source(
        "openai/codex", "ebd0765aafa255d63ebf690ebdffe0164dc14a0c",
        "codex-rs/app-server-protocol/schema/json/v2/RawResponseItemCompletedNotification.json",
        "5fb77937813629325c3f7d7aefd3e3f602c37aab901378f981df742c276de56b",
        "Apache-2.0", draft7_definitions,
        "d17f227e4df5da1600391338865ce0f3055211760a36688f816941d58232d8dc",
    ),
    "openai": Source(
        "openai/openai-openapi", "abe304d1e5f1264fd3a11499ab57c0d17f5c1df3", "openapi.json",
        "c36d229258a3ba21d62830d11a3e8c67b487490fbb8305f36e6c79b0ff943448",
        "MIT", openapi(json.loads),
    ),
    "ollama": Source(
        "ollama/ollama", "924cc286cc592f49c481fa84e15fa60dbc75989e", "docs/openapi.yaml",
        "d8e08be41dd858c933c32bef5ee8d0c5faf7d7462069a49b368731cfbceaab03",
        "MIT", openapi(yaml.safe_load),
    ),
}

# SDK release numbers are not evidence of source identity. Compare every
# production Python module in the installed package with the pinned archive.
# For Anthropic, also verify the released wheel against the same reference.
@dataclass(frozen=True)
class SDKSource:
    repo: str
    revision: str
    sha256: str
    module_path: str
    package: str
    version: str
    license_sha256: str
    wheel_url: str = ""
    wheel_sha256: str = ""

    @property
    def url(self):
        return f"https://codeload.github.com/{self.repo}/tar.gz/{self.revision}"

    @property
    def license_url(self):
        return f"https://raw.githubusercontent.com/{self.repo}/{self.revision}/LICENSE"


SDK_SOURCES = {
    "anthropic": SDKSource(
        "anthropics/anthropic-sdk-python", "f1edcd7d751a4cd6d72cf8b6454930d8aab2c97c",
        "91a5bdc976fba93a97769084ce8234e206fd27a7007fb25b00514062bb74af61",
        "src/anthropic/", "anthropic", "2.14.0",
        "7fc343be7ec189b803c1872267cbaf0381b12ffdb22d972b8ea7b21b262ba563",
        "https://files.pythonhosted.org/packages/5d/27/8bbae12e619497c651f35fec779389ab04e6b7b92b022f94390ec82be973/anthropic-2.14.0-py3-none-any.whl",
        "5cf986851f61c0858ac88c6f7c28490369a68ff4a89c309849605b764da0664d",
    ),
    "genai": SDKSource(
        "googleapis/python-genai", "36e297073202d937da00829aadbd85e42c32a571",
        "137cf44ac6ac35669559067923f1fd26f21acf4c0ecf9d9f719175723a4a04ab",
        "google/genai/", "google-genai", "3.6.1",
        "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30",
    ),
}

# Record direct source anchors in addition to the archive digest. The archive
# covers every referenced dependency; these entries locate the root authorities.
SDK_ANCHORS = {
    "anthropic": (
        "pyproject.toml",
        *("src/anthropic/types/" + path for path in (
            "message_create_params.py", "message.py", "raw_message_stream_event.py", "usage.py",
            "message_delta_usage.py", "model_info.py", "shared/error_response.py", "beta_error_response.py",
            "beta/message_create_params.py", "beta/beta_message.py", "beta/beta_raw_message_stream_event.py",
            "beta/beta_usage.py", "beta/beta_message_delta_usage.py", "beta/beta_model_info.py", "beta/beta_compaction_block.py",
            "beta/beta_compaction_config_param.py", "beta/beta_compact_20260112_edit_param.py",
        )),
    ),
    "genai": (
        "pyproject.toml", "google/genai/types.py", "google/genai/_common.py", "google/genai/models.py",
        *("google/genai/_gaos/types/interactions/" + path for path in (
            "createmodelinteraction.py", "createagentinteraction.py", "interaction.py", "interactionsseevent.py",
            "interactionsseeventinteraction.py", "error.py", "errorevent.py", "usage.py",
        )),
    ),
}

PYDANTIC_VERSION = "2.12.5"
GEMINI_DESCRIPTOR_TOOLS = {"grpcio-tools": "1.85.4", "protobuf": "7.36.2"}
GOOGLEAPIS_REVISION = "4b6ea83bc2d87be6f3b8ce19f070b649bd7f4638"
GOOGLEAPIS_BASE = f"https://raw.githubusercontent.com/googleapis/googleapis/{GOOGLEAPIS_REVISION}"
GEMINI_MODEL_PROTO = "google/ai/generativelanguage/v1beta/model.proto"
GEMINI_MODEL_SERVICE_PROTO = "google/ai/generativelanguage/v1beta/model_service.proto"
GOOGLEAPIS_FILES = {
    "google/ai/generativelanguage/v1beta/generative_service.proto":
        "c64e082244f326e528d474d7b78834c2439bdc49811645aa3291dfb12d4499c3",
    "google/ai/generativelanguage/v1beta/content.proto":
        "8c01c50c6d6795bf9bc0d4036386fe031feaf67e560ae17e1fbc6c5b47b625e7",
    "google/ai/generativelanguage/v1beta/safety.proto":
        "aa5d72a9cfd91411f5da3ab26a0c52a1a723311fd9ea52b32c697a0cee211707",
    GEMINI_MODEL_PROTO: "a59bc3fa01cf7869de8e5a26da6f3db3ea26c495e4a6d8e1e8f91c5b58a9c0b8",
    GEMINI_MODEL_SERVICE_PROTO: "546286783cd6338bae05aa639a8db8a364a90c7a4a7649362d5c9078c94604df",
    # Imports needed to compile ModelService and resolve its HTTP annotation.
    "google/ai/generativelanguage/v1beta/tuned_model.proto":
        "99033b8e12327f2b59152c79b667e299d9242e639234376eceeae133a118768c",
    "google/api/annotations.proto": "e79ea741cb605a65e78ca322174764a4af9fde1962c1631e12b84c4934ba9a6c",
    "google/api/client.proto": "5e9fc117290fe7d3472791543c979bec7cf4497f3fe6ffaf9de9ae99cc4f1804",
    "google/api/field_behavior.proto": "e2a1ac88c49909d4579bacbb18519085e82da32c2c7bb15c31037f3be19f667f",
    "google/api/http.proto": "ead99129aa15dd5f6233942030c72eec33bc0d7b1c7c260dc143293ef66c5b78",
    "google/api/launch_stage.proto": "68e22c9821227b4e84c7029569fce1055c21e6ccab991e0986bc75f12ef4dd82",
    "google/api/resource.proto": "815d51eada4341bf01672b474b5ec0abbea190e8df9f4aa26a0c96dbdad2993b",
    "google/longrunning/operations.proto": "5d2e7f9eeb55b8417b50e0a536a092babd32d26e4fbeb7099c6cdcda6e16db9c",
    "google/rpc/status.proto": "f5bfd262e6705c7ae73f32e0ad8ee20ce8c0a2578df8c4f76ebf76b572f295ed",
    "google/rpc/error_details.proto": "5c18d42e6924f447622534b4b7cdb8b53c0606c8ce729b912ee5edf88d1c5520",
    "google/rpc/code.proto": "83a63185b83cf7332196fc5ead0ec91b93ac207fbe91b3d5bc3d72f461b0bb31",
}

ANTHROPIC_STREAM_DOC = "https://platform.claude.com/docs/en/build-with-claude/streaming"
ANTHROPIC_COMPACTION_DOC = "https://platform.claude.com/docs/en/build-with-claude/compaction-on-demand"
GEMINI_REST_DOC = "https://ai.google.dev/api/generate-content"
PROTOJSON_DOC = "https://protobuf.dev/programming-guides/json/"
RPC_ERROR_DOC = "https://google.aip.dev/193#http11json-representation"
INTERACTIONS_REST_DOC = "https://aistudio.google.com/api-reference/interactions.openapi.json?auto_signin=false"
INTERACTIONS_ERROR_DOC = "https://aistudio.google.com/docs/api-errors"

GEMINI_MODEL_ROOTS = ("Model", "ListModelsResponse")

# Corroboration consulted during development, not a generation input.
REST_EVIDENCE = {
    "interactions": {
        "url": INTERACTIONS_REST_DOC,
        "revision": "v1beta / x-google-revision: 0 (observed 2026-11-25 UTC)",
        "sha256": "76210b6440354784c0dec1c92d0013b3e7bfe24e6b1a324b62eed91bf58e5fa2",
        "notes": "UNVERIFIED: immutable OpenAPI snapshot URL and redistribution license. No OpenAPI bytes are embedded.",
    },
}


# OpenAPI wires and externally addressable schema roots.
WIRES = {
    "codex": ("codex", ("ResponseItem",)),
    "openai-completions": ("openai", ("CreateCompletionRequest", "CreateCompletionResponse", "ErrorResponse")),
    "chat": ("openai", (
        "CreateChatCompletionRequest", "CreateChatCompletionResponse",
        "CreateChatCompletionStreamResponse", "ErrorResponse", "ListModelsResponse",
    )),
    "responses": ("openai", (
        "CreateResponse", "Response", "ResponseStreamEvent", "ErrorResponse", "ListModelsResponse",
        "CompactResponseMethodPublicBody", "CompactResource",
    )),
    "ollama": ("ollama", (
        "ChatRequest", "ChatResponse", "ChatStreamEvent", "ErrorResponse", "ListResponse", "ShowRequest", "ShowResponse",
    )),
}

# Walk only schema locations, not arbitrary JSON in enum/const/default/examples.
SCHEMA_MAPS = {"properties", "patternProperties", "dependentSchemas", "$defs"}
SCHEMA_LISTS = {"allOf", "anyOf", "oneOf", "prefixItems"}
SCHEMA_VALUES = {
    "additionalProperties", "unevaluatedProperties", "propertyNames", "items",
    "unevaluatedItems", "contains", "if", "then", "else", "not", "contentSchema",
}
# Documentation and examples do not affect validation or path diagnostics.
OMIT = {"description", "title", "example", "examples", "externalDocs", "x-oaiMeta", "x-stainless-example"}


def bundle(definitions, roots):
    closure = {}
    references = {}

    def include(name):
        if name not in closure:
            closure[name] = None  # a recursive reference can revisit this entry
            references[name] = set()
            closure[name] = extract(definitions[name], references[name])

    def ref(value, referenced):
        for prefix in (OAS_COMPONENTS, DEFS, DRAFT7_DEFS):
            if value.startswith(prefix):
                pointer = value[len(prefix):]
                name = unquote(pointer).split("/", 1)[0].replace("~1", "/").replace("~0", "~")
                referenced.add(name)
                include(name)
                return DEFS + pointer
        raise ValueError(f"reference outside the named schema definitions: {value}")

    def extract(schema, referenced):
        if isinstance(schema, bool):
            return schema
        result = {}
        for key, value in schema.items():
            if key in OMIT:
                continue
            if key in {"$id", "$dynamicRef", "$schema"}:
                raise ValueError(f"nested {key} needs an explicit resource adapter")
            if key == "$ref":
                value = ref(value, referenced)
            elif key in SCHEMA_MAPS:
                value = {name: extract(child, referenced) for name, child in value.items()}
            elif key in SCHEMA_LISTS:
                value = [extract(child, referenced) for child in value]
            elif key in SCHEMA_VALUES:
                value = extract(value, referenced)
            elif key == "discriminator" and "mapping" in value:
                value = {**value, "mapping": {
                    label: ref(target if target.startswith("#") else DEFS + target, referenced)
                    for label, target in value["mapping"].items()
                }}
            result[key] = value
        return result

    for name in roots:
        include(name)

    def dependencies(root):
        visited = set()
        pending = [root]
        while pending:
            name = pending.pop()
            if name not in visited:
                visited.add(name)
                pending.extend(references[name] - visited)
        return sorted(visited)

    return {"$schema": DIALECT, "$defs": closure}, {root: dependencies(root) for root in roots}


# jsonschema-go uses Go regexp and rejects ECMAScript lookahead. This one
# pinned assertion forbids CR/LF and requires a nonempty name in the given
# ASCII set. Compose the same two assertions without regex lookahead, and fail
# generation if the upstream assertion changes.
def normalize_openai_patterns(schema):
    component = "ToolSearchOutputFunctionToolParam"
    if component not in schema["$defs"]:
        return []
    name = schema["$defs"][component]["properties"]["name"]
    if name["pattern"] != r"^(?![\s\S]*[\r\n])[a-zA-Z0-9_.-]+$" or "not" in name:
        raise ValueError(f"recheck upstream pattern for {component}.name")
    name["pattern"] = r"^[a-zA-Z0-9_.-]+$"
    name["not"] = {"pattern": r"[\r\n]"}
    return [f"{DEFS}{component}/properties/name: express the CR/LF exclusion as a separate not assertion"]


def normalize_openai_input_item(schema):
    name = "InputItem"
    if name not in schema["$defs"]:
        return []
    item = schema["$defs"][name]
    expected = [
        {"$ref": DEFS + "EasyInputMessage"}, {"$ref": DEFS + "Item", "type": "object"},
        *({"$ref": DEFS + arm} for arm in (
            "CompactionTriggerItemParam", "ItemReferenceParam", "ProgramItemParam", "ProgramOutputItemParam",
        )),
    ]
    if item.get("oneOf") != expected or "anyOf" in item:
        raise ValueError(f"recheck upstream {name} union")
    item["anyOf"] = item.pop("oneOf")
    return [{
        "pointer": "#/components/schemas/InputItem/oneOf",
        "change": "Use anyOf: EasyInputMessage and Item/InputMessage both accept the witness. Preserve all arm assertions.",
        "witness": {"role": "user", "content": [{"type": "input_text", "text": "Hello"}]},
        "authority": "https://github.com/openai/openai-python/blob/aff223de836de6781e538ce21badfd4620961544/src/openai/types/responses/response_input_param.py",
    }]


def completion_definitions(definitions):
    # Both media types reference CreateCompletionResponse. Its usage $ref
    # only permits objects, but include_usage explicitly requires null on
    # ordinary stream chunks. Keep the non-streaming root unchanged.
    root = definitions["CreateCompletionResponse"]
    usage = root["properties"]["usage"]
    if usage != {"$ref": OAS_COMPONENTS + "CompletionUsage"}:
        raise ValueError("recheck CreateCompletionResponse.usage")
    stream = {**root, "properties": {**root["properties"], "usage": {"anyOf": [usage, {"type": "null"}]}}}
    return {**definitions, "CompletionStreamChunk": stream}, [{
        "pointer": "#/components/schemas/CreateCompletionResponse/properties/usage",
        "change": "Stream-only root: allow null as specified by ChatCompletionStreamOptions.include_usage. Keep the JSON response root unchanged.",
    }]


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def fetch(cache, url, sha256=None):
    path = cache / f"{digest(url.encode())}-{Path(url).name}"
    cached = path.exists()
    if cached:
        raw = path.read_bytes()
    else:
        with urllib.request.urlopen(url, timeout=45) as response:
            raw = response.read()
    if sha256 and digest(raw) != sha256:
        raise ValueError(f"raw SHA-256 mismatch for {url}; remove a corrupt cache entry: {path}")
    if not cached:
        # A parallel generator must never see a partially saved download.
        with tempfile.NamedTemporaryFile(dir=cache, delete=False) as pending:
            candidate = Path(pending.name)
            try:
                pending.write(raw)
                pending.close()
                candidate.replace(path)
            finally:
                candidate.unlink(missing_ok=True)
    return raw


def json_bytes(value, *, compact=False):
    return (json.dumps(value, ensure_ascii=False, sort_keys=True,
                       separators=(",", ":") if compact else None,
                       indent=None if compact else 2, allow_nan=False) + "\n").encode("utf-8")


def verify_sdk(cache, name, source, outputs):
    try:
        installed = distribution(source.package)
    except PackageNotFoundError as error:
        raise ValueError("install scripts/providersim_requirements.txt in the generator environment") from error
    if installed.version != source.version:
        raise ValueError(f"need {source.package}=={source.version}, found {installed.version}")
    archive = fetch(cache, source.url, source.sha256)
    prefix = source.module_path.removeprefix("src/")

    def production_module(path):
        return path.startswith(prefix) and path.endswith(".py") and "/tests/" not in path

    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:gz") as tar:
        expected = {}
        anchors = {}
        for member in tar:
            if not member.isfile():
                continue
            path = member.name.split("/", 1)[1]
            module = path.removeprefix("src/")
            anchor = path in SDK_ANCHORS[name]
            production = production_module(module)
            if anchor or production:
                extracted = tar.extractfile(member)
                if extracted is None:
                    raise ValueError(f"cannot read archived file {path} from {source.url}")
                with extracted:
                    sha256 = digest(extracted.read())
                if anchor:
                    anchors[path] = {"url": f"https://raw.githubusercontent.com/{source.repo}/{source.revision}/{path}",
                                     "sha256": sha256}
                if production:
                    expected[module] = sha256
    if anchors.keys() != set(SDK_ANCHORS[name]):
        raise ValueError(f"missing source anchors in {source.url}")
    if not expected:
        raise ValueError(f"no Python modules found in {source.url}")
    installed_hashes = {
        path: digest(entry.read_binary())
        for entry in installed.files or () if production_module(path := entry.as_posix())
    }
    if installed_hashes != expected:
        changed = sorted(path for path in expected.keys() | installed_hashes.keys()
                         if expected.get(path) != installed_hashes.get(path))
        raise ValueError(f"installed {source.package} differs from pinned Git source: {', '.join(changed[:5])}")
    license_file = f"{name}.license.txt"
    outputs[license_file] = fetch(cache, source.license_url, source.license_sha256).replace(b"\r\n", b"\n")
    record = {
        "url": source.url, "revision": source.revision, "sha256": source.sha256,
        "package": source.package, "package_version": source.version, "verified_modules": len(expected),
        "files": anchors,
        "license": "Apache-2.0", "license_url": source.license_url,
        "license_file": license_file, "license_sha256": source.license_sha256,
        "license_output_sha256": digest(outputs[license_file]),
    }
    if source.wheel_url:
        wheel = fetch(cache, source.wheel_url, source.wheel_sha256)
        with zipfile.ZipFile(io.BytesIO(wheel)) as z:
            hashes = {path: digest(z.read(path)) for path in z.namelist() if production_module(path)}
        if hashes != expected:
            raise ValueError(f"PyPI {source.package} wheel does not match the pinned Git source")
        record["pypi"] = {"url": source.wheel_url, "sha256": source.wheel_sha256, "matches_revision": True}
    else:
        record["pypi"] = {
            "url": "https://files.pythonhosted.org/packages/c0/1a/576adc030b48c7491b33ab94b96521151ef5b956552c0057628ef39c69bb/google_genai-3.6.1-py3-none-any.whl",
            "sha256": "4b35064da644f61d7e5f6c0e9406bf63b945e7e96b4af18cadb3371cbc3acf74",
            "matches_revision": False,
            "notes": "The released 3.6.1 wheel predates the pin and differs in generated Interactions types. Install the Git revision.",
        }
    return record


def googleapis(cache, outputs):
    base = GOOGLEAPIS_BASE
    files = {path: fetch(cache, f"{base}/{path}", sha256) for path, sha256 in GOOGLEAPIS_FILES.items()}
    license_url = base + "/LICENSE"
    license_sha256 = "c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4"
    license_file = "googleapis.license.txt"
    outputs[license_file] = fetch(cache, license_url, license_sha256).replace(b"\r\n", b"\n")
    return files, {
        "url": base, "revision": GOOGLEAPIS_REVISION,
        "files": {path: {"url": f"{base}/{path}", "sha256": digest(raw)} for path, raw in files.items()},
        "license": "Apache-2.0", "license_url": license_url,
        "license_file": license_file, "license_sha256": license_sha256,
        "license_output_sha256": digest(outputs[license_file]),
    }


def local_ref(name):
    return {"$ref": DEFS + name}


def object_schema(properties, *required):
    return {"type": "object", "properties": properties, "required": list(required)}


def sdk_schema(models: dict[str, Any], *, mode: JsonSchemaMode = "validation"):
    from pydantic import TypeAdapter

    keys: list[tuple[str, JsonSchemaMode, TypeAdapter[Any]]] = [
        (key, mode, TypeAdapter(model)) for key, model in models.items()
    ]
    roots, schema = TypeAdapter.json_schemas(keys, by_alias=True)
    definitions = schema.get("$defs", {})
    for (name, _), root in roots.items():
        # Most BaseModel roots have their own named definition; TypedDicts,
        # aliases and unions need a simulator-facing name.
        if root != local_ref(name):
            if name in definitions:
                raise ValueError(f"Pydantic root name collides with another definition: {name}")
            definitions[name] = root
    return definitions


def resolve_local(definitions, schema):
    while schema.get("$ref", "").startswith(DEFS):
        schema = definitions[schema["$ref"][len(DEFS):]]
    return schema


def message_definitions():
    from anthropic import types as a
    from anthropic.types import beta
    from anthropic.types.beta import message_create_params as beta_request
    from anthropic.types.shared import ErrorResponse

    models = {
        "MessageCreateParams": a.message_create_params.MessageCreateParams,
        "BetaMessageCreateParams": beta_request.MessageCreateParams,
        "Message": a.Message, "BetaMessage": beta.BetaMessage,
        "ModelInfo": a.ModelInfo, "BetaModelInfo": beta.BetaModelInfo,
        "RawMessageStreamEvent": a.RawMessageStreamEvent, "BetaRawMessageStreamEvent": beta.BetaRawMessageStreamEvent,
        "ErrorResponse": ErrorResponse, "BetaErrorResponse": a.BetaErrorResponse,
        "Usage": a.Usage, "BetaUsage": beta.BetaUsage,
        "MessageDeltaUsage": a.MessageDeltaUsage, "BetaMessageDeltaUsage": beta.BetaMessageDeltaUsage,
        "BetaCompactionBlock": beta.BetaCompactionBlock,
        "BetaCompactionConfigParam": beta.BetaCompactionConfigParam,
        "BetaCompact20260112EditParam": beta.BetaCompact20260112EditParam,
    }
    definitions = sdk_schema(models)
    request = definitions["BetaMessageCreateParams"]
    for branch in request["anyOf"]:
        # The SDK exposes the HTTP anthropic-beta header as a method argument.
        props = resolve_local(definitions, branch)["properties"]
        props.pop("betas")
        if not {"compaction", "context_management"} <= props.keys():
            raise ValueError("recheck Anthropic compaction request fields")
    request["not"] = {"required": ["compaction", "context_management"]}
    definitions["PingEvent"] = object_schema({"type": {"const": "ping"}}, "type")
    for prefix in ("", "Beta"):
        definitions[f"{prefix}MessageStreamEvent"] = {"anyOf": [
            local_ref(f"{prefix}RawMessageStreamEvent"), local_ref("PingEvent"), local_ref(f"{prefix}ErrorResponse"),
        ]}
    return definitions, (*models, "MessageStreamEvent", "BetaMessageStreamEvent", "PingEvent"), [
        {"pointer": f"{DEFS}BetaMessageCreateParams", "authority": ANTHROPIC_COMPACTION_DOC,
         "change": "Exclude SDK betas (HTTP header). Reject simultaneous compaction and context_management."},
        {"pointer": f"{DEFS}MessageStreamEvent / BetaMessageStreamEvent", "authority": ANTHROPIC_STREAM_DOC,
         "change": "Add documented ping and error events outside the SDK's six-event union."},
    ]


def gemini_model_definitions(protos):
    from google.protobuf import descriptor_pb2, descriptor_pool, message_factory
    from google.protobuf.descriptor import FieldDescriptor
    from grpc_tools import protoc

    with tempfile.TemporaryDirectory(prefix="providersim-protos-") as temp:
        root = Path(temp)
        for name, raw in protos.items():
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(raw)
        output = root / "inventory.pb"
        include = package_files("grpc_tools").joinpath("_proto")
        if protoc.main([
            "protoc", f"-I{root}", f"-I{include}", "--include_imports",
            f"--descriptor_set_out={output}", GEMINI_MODEL_SERVICE_PROTO,
        ]) != 0:
            raise ValueError("cannot compile pinned Gemini model service")
        compiled = descriptor_pb2.FileDescriptorSet.FromString(output.read_bytes())

    pool = descriptor_pool.DescriptorPool()
    for file in compiled.file:
        pool.Add(file)
    package = "google.ai.generativelanguage.v1beta."
    method = pool.FindMethodByName(package + "ModelService.ListModels")
    http = pool.FindExtensionByName("google.api.http")
    options_type, rule_type = http.containing_type, http.message_type
    if options_type is None or rule_type is None:
        raise ValueError("recheck the google.api.http annotation type")
    # Decode options with the pool that knows the google.api.http extension.
    options = message_factory.GetMessageClass(options_type).FromString(method.GetOptions().SerializeToString())
    expected_http = message_factory.GetMessageClass(rule_type)(get="/v1beta/models")
    # The pool returns a FieldDescriptor for extension handles, too.
    binding = options.Extensions[http]  # pyright: ignore[reportArgumentType]
    if (binding != expected_http or method.output_type.full_name != package + "ListModelsResponse"
            or method.client_streaming or method.server_streaming):
        raise ValueError("recheck the Gemini model listing HTTP binding")

    scalars = {
        FieldDescriptor.TYPE_STRING: {"type": "string"},
        FieldDescriptor.TYPE_BOOL: {"type": "boolean"},
        FieldDescriptor.TYPE_INT32: {"type": "integer", "format": "int32"},
        FieldDescriptor.TYPE_FLOAT: {"type": "number", "format": "float"},
    }
    definitions = {}
    for name in GEMINI_MODEL_ROOTS:
        properties = {}
        for field in pool.FindMessageTypeByName(package + name).fields:
            message_type = field.message_type
            if field.type == FieldDescriptor.TYPE_MESSAGE and message_type is not None and message_type.full_name == package + "Model":
                schema = local_ref("Model")
            elif field.type in scalars:
                schema = scalars[field.type]
            else:
                raise ValueError(f"unsupported Gemini inventory field type: {field.full_name}")
            properties[field.json_name] = {"type": "array", "items": schema} if field.is_repeated else schema
        # Preserve the REST response shape: no required keys, including for
        # default-valued fields and empty pages. This is not a ProtoJSON parser.
        definitions[name] = {"type": "object", "properties": properties}
    return definitions


def gemini_definitions(protos):
    from google.genai import types as g
    from pydantic import BaseModel, create_model

    # GenerateContentConfig mixes Developer API body fields with settings for
    # the client or Vertex. The pinned _GenerateContentConfig_to_mldev converter
    # assigns these exact destinations. Content shorthands are already expanded.
    generation = (
        "temperature", "top_p", "top_k", "candidate_count", "max_output_tokens", "stop_sequences",
        "response_logprobs", "logprobs", "presence_penalty", "frequency_penalty", "seed",
        "response_mime_type", "response_schema", "response_json_schema", "response_modalities",
        "media_resolution", "speech_config", "thinking_config", "image_config", "enable_enhanced_civic_answers",
        "audio_transcription_config",
    )
    body_fields = ("tool_config", "safety_settings", "labels", "cached_content", "service_tier", "continuation_token", "store")

    service = protos["google/ai/generativelanguage/v1beta/generative_service.proto"]
    if b"optional int32 top_k =" not in service:
        raise ValueError("recheck generateContent topK type")

    def project(name: str, selected: tuple[str, ...], **overrides: Any) -> type[BaseModel]:
        if any(field.startswith("__") for field in selected):
            raise ValueError(f"reserved Pydantic keyword in {name} projection")
        fields = g.GenerateContentConfig.model_fields
        # Pydantic accepts dynamic field definitions as Any; the guard above
        # excludes its separately typed, dunder-prefixed configuration keys.
        projected: dict[str, Any] = {
            field: (overrides.get(field, fields[field].annotation), fields[field]) for field in selected
        }
        return create_model(name, **projected)

    models = {
        "GenerateContentResponse": g.GenerateContentResponse,
        "GenerateContentResponseUsageMetadata": g.GenerateContentResponseUsageMetadata,
        "GenerationConfig": project("GenerationConfig", generation, response_schema=g.Schema | None,
                                    speech_config=g.SpeechConfig | None, top_k=int | None),
        "GenerateContentBodyFields": project("GenerateContentBodyFields", (*body_fields, "tools"), tools=list[g.Tool] | None),
        "Content": g.Content,
    }
    definitions = sdk_schema(models, mode="serialization")
    response = resolve_local(definitions, definitions["GenerateContentResponse"])["properties"]
    for field in ("sdkHttpResponse", "createTime", "automaticFunctionCallingHistory", "parsed"):
        response.pop(field)
    request_props = resolve_local(definitions, definitions["GenerateContentBodyFields"])["properties"]
    request_props.update({
        "contents": {"type": "array", "items": local_ref("Content")},
        "systemInstruction": local_ref("Content"),
        "generationConfig": local_ref("GenerationConfig"),
    })
    definitions["GenerateContentRequest"] = object_schema(request_props, "contents")
    # The usage definition keeps the SDK name too, for drift checks and codecs.
    definitions["GenerateContentUsageMetadata"] = local_ref("GenerateContentResponseUsageMetadata")
    definitions.update(rpc_error_definitions(protos))
    inventory = gemini_model_definitions(protos)
    if definitions.keys() & inventory.keys():
        raise ValueError("Gemini inventory definitions collide with SDK schema names")
    definitions.update(inventory)
    return definitions, (
        "GenerateContentRequest", "GenerateContentResponse", "GenerationConfig",
        "GenerateContentResponseUsageMetadata", "GenerateContentUsageMetadata", "ErrorResponse", "RetryInfo",
        *GEMINI_MODEL_ROOTS,
    ), [
        {"pointer": f"{DEFS}GenerateContentRequest / GenerationConfig", "authority": GEMINI_REST_DOC,
         "change": "Emit REST aliases and Developer API fields. Model is a URL parameter. Expand Content shorthands and use protobuf int32 topK, not the SDK's float convenience type."},
        {"pointer": f"{DEFS}GenerateContentResponse", "authority": GEMINI_REST_DOC,
         "change": "Remove SDK transport/client fields and the Vertex-only createTime field."},
        {"pointer": f"{DEFS}ErrorResponse / RetryInfo", "authority": RPC_ERROR_DOC,
         "change": "Use HTTP error code and string google.rpc.Code status, not the raw Status protobuf. Dispatch RetryInfo by @type and use ProtoJSON Duration seconds."},
        {"pointer": f"{DEFS}Model / ListModelsResponse", "authority": f"{GOOGLEAPIS_BASE}/{GEMINI_MODEL_SERVICE_PROTO}",
         "change": "Compile the pinned GET /v1beta/models binding and project the two response descriptors. Use Protobuf JSON names, scalar formats and repeated arrays without requiring fields in responses."},
    ]


def rpc_error_definitions(protos):
    code_proto = protos["google/rpc/code.proto"].decode("utf-8")
    _, enum = code_proto.split("enum Code {", 1)
    codes = re.findall(r"^  ([A-Z][A-Z_]+) = \d+;", enum.split("\n}", 1)[0], re.MULTILINE)
    if not {"INVALID_ARGUMENT", "RESOURCE_EXHAUSTED", "UNAVAILABLE"} <= set(codes):
        raise ValueError("recheck google.rpc.Code enum")
    if b"google.protobuf.Duration retry_delay = 1;" not in protos["google/rpc/error_details.proto"]:
        raise ValueError("recheck google.rpc.RetryInfo proto")
    string = {"type": "string"}
    type_url = "type.googleapis.com/google.rpc.RetryInfo"
    retry_type = object_schema({"@type": {"const": type_url}}, "@type")
    retry = object_schema({
        "@type": {"const": type_url}, "retryDelay": {"type": "string", "pattern": r"^-?[0-9]+(?:\.[0-9]{1,9})?s$"},
    }, "@type", "retryDelay")
    detail = object_schema({"@type": string}, "@type")
    detail["not"] = retry_type
    error = object_schema({
        "code": {"type": "integer"}, "message": string, "status": {"enum": codes},
        "details": {"type": "array", "items": {"oneOf": [local_ref("RetryInfo"), detail]}},
    }, "code", "message", "status")
    return {"ErrorResponse": object_schema({"error": error}, "error"), "RetryInfo": retry}


def without_options(schema, *options):
    arms = list(schema["anyOf"])
    for option in options:
        arms.remove(option)
    if not arms:
        raise ValueError("wire projection removed every SDK alternative")
    if len(arms) == 1:
        return arms[0]
    return {"anyOf": arms}


def interaction_definitions():
    from google.genai._gaos.types.interactions import Interaction, Usage
    from typing import Union, get_args

    from google.genai._gaos.types.interactions.createagentinteraction import CreateAgentInteraction
    from google.genai._gaos.types.interactions.createmodelinteraction import CreateModelInteraction
    from google.genai._gaos.types.interactions.error import Error
    from google.genai._gaos.types.interactions.interactionsseevent import InteractionSSEEvent, UnknownInteractionSSEEvent

    models = {
        "CreateModelInteraction": CreateModelInteraction,
        "CreateAgentInteraction": CreateAgentInteraction,
        "Interaction": Interaction,
        "InteractionSSEEvent": Union[tuple(variant for variant in get_args(get_args(InteractionSSEEvent)[0])
                                           if variant is not UnknownInteractionSSEEvent)],
        "Error": Error,
        "Usage": Usage,
    }
    definitions = sdk_schema(models)
    for root, name, forbidden in (
        ("CreateModelInteraction", "model", "agent"), ("CreateAgentInteraction", "agent", "model"),
    ):
        branch = resolve_local(definitions, definitions[root])
        if name not in branch["required"] or forbidden in branch["properties"]:
            raise ValueError(f"recheck Interactions {root} identifier")
        branch["not"] = {"required": [forbidden]}
        if name == "agent" and "input" not in branch["required"]:
            branch["required"].append("input")
            branch["properties"]["input"] = without_options(branch["properties"]["input"], {"type": "null"})
    definitions["CreateInteraction"] = {"oneOf": [local_ref("CreateModelInteraction"), local_ref("CreateAgentInteraction")]}
    full = resolve_local(definitions, definitions["Interaction"])
    for field in ("created", "id", "steps", "updated"):
        full["required"].append(field)
        full["properties"][field] = without_options(full["properties"][field], {"type": "null"})
    generation = resolve_local(definitions, definitions["GenerationConfig"])["properties"]
    for field in ("thinking_level", "thinking_summaries"):
        generation[field] = without_options(generation[field], {"type": "string"}, {"type": "null"})
    # The SDK preserves unknown variants for forward compatibility when reading
    # a response. This generator validates what the simulator is allowed to emit.
    events = resolve_local(definitions, definitions["InteractionSSEEvent"])
    known = {"interaction.created", "interaction.status_update", "interaction.completed", "step.start", "step.delta", "step.stop", "error"}
    variants = [resolve_local(definitions, arm) for arm in events["anyOf"]]
    if {variant["properties"]["event_type"]["const"] for variant in variants} != known:
        raise ValueError("recheck documented Interactions SSE events")
    for variant in variants:
        required = variant.setdefault("required", [])
        if "event_type" not in required:
            required.append("event_type")
    events["oneOf"] = events.pop("anyOf")
    error = resolve_local(definitions, definitions["Error"])
    error["required"] = ["code", "message"]
    for field in error["required"]:
        error["properties"][field] = {"type": "string"}
    definitions["ErrorResponse"] = object_schema({"error": local_ref("Error")}, "error")
    return definitions, (*models, "CreateInteraction", "ErrorResponse"), [
        {"pointer": f"{DEFS}CreateInteraction", "authority": INTERACTIONS_REST_DOC,
         "change": "Use oneOf for separate model and agent forms; prohibit both identifiers and require agent input."},
        {"pointer": f"{DEFS}Interaction", "authority": INTERACTIONS_REST_DOC,
         "change": "Require id, created, updated and steps on full HTTP responses, not on partial lifecycle event payloads."},
        {"pointer": f"{DEFS}GenerationConfig", "authority": INTERACTIONS_REST_DOC,
         "change": "Use published lowercase thinking_level and thinking_summaries enums, not the SDK's open-string reader fallback."},
        {"pointer": f"{DEFS}InteractionSSEEvent", "authority": INTERACTIONS_REST_DOC,
         "change": "Use the seven published event_type variants, not the SDK's client-only UNKNOWN fallback. Each tag is distinct."},
        {"pointer": f"{DEFS}ErrorResponse / Error", "authority": INTERACTIONS_ERROR_DOC,
         "change": "Require documented string code and message; wrap the inner SDK error as an HTTP error object."},
    ]


def generate(cache):
    from pydantic import __version__ as pydantic_version

    if pydantic_version != PYDANTIC_VERSION:
        raise ValueError(f"need pydantic=={PYDANTIC_VERSION}, found {pydantic_version}")
    for package, expected in GEMINI_DESCRIPTOR_TOOLS.items():
        found = distribution(package).version
        if found != expected:
            raise ValueError(f"need {package}=={expected}, found {found}")
    manifest = {"generator_version": VERSION, "pydantic_version": PYDANTIC_VERSION,
                "gemini_descriptor_tools": GEMINI_DESCRIPTOR_TOOLS, "sources": {}, "bundles": {}}
    outputs = {}
    definitions = {}
    for name, source in SOURCES.items():
        url = source.url(source.path)
        license_url = source.url("LICENSE")
        license_file = f"{name}.license.txt"
        raw = fetch(cache, url, source.sha256)
        license_raw = fetch(cache, license_url, source.license_sha256)
        # The license notice is redistributed with extracts of the upstream work.
        outputs[license_file] = license_raw.replace(b"\r\n", b"\n")
        definitions[name] = source.definitions(raw)
        manifest["sources"][name] = {
            "url": url, "revision": source.revision, "sha256": digest(raw),
            "license": source.license, "license_url": license_url, "license_file": license_file,
            "license_sha256": digest(license_raw), "license_output_sha256": digest(outputs[license_file]),
        }
    for name, source in SDK_SOURCES.items():
        manifest["sources"][name] = verify_sdk(cache, name, source, outputs)
    protos, manifest["sources"]["googleapis"] = googleapis(cache, outputs)

    def emit(wire, source, definitions, roots, adaptations, **evidence):
        filename = f"{wire}.json"
        schema, dependencies = bundle(definitions, roots)
        if source == "openai":
            adaptations.extend(normalize_openai_patterns(schema))
            adaptations.extend(normalize_openai_input_item(schema))
        raw = json_bytes(schema, compact=True)
        outputs[filename] = raw
        manifest["bundles"][filename] = {
            "source": source, "roots": dependencies, "components": sorted(schema["$defs"]),
            "sha256": digest(raw), "adaptations": adaptations, **evidence,
        }

    for wire, (source, roots) in WIRES.items():
        if wire == "openai-completions":
            defs, adaptations = completion_definitions(definitions[source])
            emit(wire, source, defs, (*roots, "CompletionStreamChunk"), adaptations)
        elif source == "codex":
            emit(wire, source, definitions[source], roots, [{
                "pointer": "#/definitions/ResponseItem",
                "change": "Equivalent Draft 7 subset to Draft 2020-12; relink definitions to $defs. No assertions changed.",
            }], notes=["UNVERIFIED: private backend request envelope and server SSE contract; this is its first-party shared ResponseItem type only."])
        else:
            emit(wire, source, definitions[source], roots, [])
    emit("messages", "anthropic", *message_definitions(), notes=[
        "UNVERIFIED: separately downloadable licensed Anthropic OpenAPI spec and exact server behavior for every header combination.",
    ])
    emit("gemini", "genai", *gemini_definitions(protos), additional_sources=["googleapis"],
         notes=[
             "UNVERIFIED: exact in-band error frame for HTTP 200 streamGenerateContent?alt=sse.",
             f"REST types use {PROTOJSON_DOC} aliases; the separately versioned Developer API, not Vertex, is the target.",
             "UNVERIFIED at SDK pin: discovery-only additions not emitted by the pinned Developer API converter, such as translationConfig.",
         ])
    emit("interactions", "genai", *interaction_definitions(), rest_evidence=REST_EVIDENCE["interactions"], notes=[
        "UNVERIFIED: exact context overflow or retry hints without a sanitized service recording.",
        "The pinned SDK remains the authority for metadata outside the documented projections, including open lifecycle status strings.",
    ])
    outputs["manifest.json"] = json_bytes(manifest)
    return outputs


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    default_cache = Path(os.environ.get("CRUDE_TEMP_DIR") or tempfile.gettempdir()) / "providersim-sources"
    parser.add_argument("--cache", type=Path, default=default_cache, help="source cache outside the repository")
    parser.add_argument("--check", action="store_true", help="compare without updating generated files")
    args = parser.parse_args()
    cache = args.cache.resolve()
    if cache.is_relative_to(ROOT):
        parser.error("the source cache must be outside the repository")
    cache.mkdir(parents=True, exist_ok=True)
    try:
        outputs = generate(cache)
    except (ValueError, OSError, KeyError, ImportError) as error:
        parser.exit(1, f"cannot generate simulator schemas: {error}\n")
    if args.check:
        mismatches = [name for name, raw in outputs.items()
                      if not (OUT / name).is_file() or (OUT / name).read_bytes() != raw]
        if mismatches:
            parser.exit(1, "outdated simulator schemas: " + ", ".join(mismatches) + "\n")
    else:
        OUT.mkdir(parents=True, exist_ok=True)
        for name, raw in outputs.items():
            (OUT / name).write_bytes(raw)


if __name__ == "__main__":
    main()
