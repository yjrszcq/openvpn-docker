(() => {
  "use strict";

  const methods = ["get", "post", "put", "patch", "delete"];
  const sampleUUID = "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e";
  const statusText = {200: "OK", 201: "Created", 400: "Bad Request", 401: "Unauthorized", 404: "Not Found", 405: "Method Not Allowed", 409: "Conflict", 422: "Unprocessable Entity", 500: "Internal Server Error", 503: "Service Unavailable"};
  let documentRoot;

  const element = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };

  const resolve = value => {
    if (!value || typeof value !== "object" || !value.$ref) return value || {};
    return value.$ref.slice(2).split("/").reduce((current, part) => current[part], documentRoot);
  };

  const schemaType = input => {
    const schema = resolve(input);
    if (schema.oneOf) return schema.oneOf.map(schemaType).join(" | ");
    if (schema.anyOf) return schema.anyOf.map(schemaType).join(" | ");
    if (schema.enum) return schema.enum.map(value => JSON.stringify(value)).join(" | ");
    if (Array.isArray(schema.type)) return schema.type.join(" | ");
    if (schema.type === "array") return `${schemaType(schema.items)}[]`;
    if (schema.type === "object" || schema.properties) return "object";
    return schema.format ? `${schema.type || "string"} (${schema.format})` : (schema.type || "any");
  };

  const schemaLines = (input, depth = 0, seen = new Set()) => {
    const schema = resolve(input);
    if (depth > 5) return ["  ".repeat(depth) + schemaType(schema)];
    const key = input && input.$ref;
    if (key && seen.has(key)) return ["  ".repeat(depth) + schemaType(schema)];
    const nextSeen = new Set(seen);
    if (key) nextSeen.add(key);
    if (schema.oneOf || schema.anyOf) {
      const variants = schema.oneOf || schema.anyOf;
      return variants.flatMap((variant, index) => ["  ".repeat(depth) + `variant ${index + 1}:`, ...schemaLines(variant, depth + 1, nextSeen)]);
    }
    if (schema.type === "array") return ["  ".repeat(depth) + "array of:", ...schemaLines(schema.items, depth + 1, nextSeen)];
    if (schema.type !== "object" && !schema.properties) return ["  ".repeat(depth) + schemaType(schema)];
    const required = new Set(schema.required || []);
    const lines = [];
    Object.entries(schema.properties || {}).forEach(([name, property]) => {
      const resolved = resolve(property);
      const marker = required.has(name) ? "required" : "optional";
      const description = resolved.description ? ` - ${resolved.description}` : "";
      lines.push(`${"  ".repeat(depth)}${name}: ${schemaType(property)} [${marker}]${description}`);
      if ((resolved.type === "object" || resolved.properties || resolved.type === "array" || resolved.oneOf) && depth < 3) {
        lines.push(...schemaLines(property, depth + 1, nextSeen));
      }
    });
    return lines.length ? lines : ["  ".repeat(depth) + "object"];
  };

  const sample = (input, depth = 0, seen = new Set()) => {
    const schema = resolve(input);
    if (schema.example !== undefined) return schema.example;
    if (schema.default !== undefined) return schema.default;
    if (schema.enum) return schema.enum[0];
    if (schema.const !== undefined) return schema.const;
    if (depth > 6) return null;
    const key = input && input.$ref;
    if (key && seen.has(key)) return null;
    const nextSeen = new Set(seen);
    if (key) nextSeen.add(key);
    if (schema.oneOf) return sample(schema.oneOf[0], depth + 1, nextSeen);
    if (schema.anyOf) return sample(schema.anyOf[0], depth + 1, nextSeen);
    if (schema.type === "object" || schema.properties) {
      const value = {};
      Object.entries(schema.properties || {}).forEach(([name, property]) => { value[name] = sample(property, depth + 1, nextSeen); });
      return value;
    }
    if (schema.type === "array") return [sample(schema.items, depth + 1, nextSeen)];
    if (schema.type === "integer" || schema.type === "number") return schema.minimum || 0;
    if (schema.type === "boolean") return false;
    if (schema.format === "uuid") return sampleUUID;
    if (schema.format === "date-time") return "2026-08-19T09:55:07Z";
    if (schema.format === "ipv4") return "10.42.0.30";
    if ((schema.pattern || "").includes("[0-9a-f]{64}")) return "489b950c0d134b5d7e8f350b2cb4bfa4d6d163d841f840964baf6d74c65cc8ca";
    return schema.type === "null" ? null : "string";
  };

  const mediaExample = media => {
    if (media.example !== undefined) return media.example;
    if (media.examples) {
      const first = Object.values(media.examples)[0];
      if (first && first.value !== undefined) return first.value;
    }
    return sample(media.schema || {});
  };

  const pretty = value => typeof value === "string" ? value : JSON.stringify(value, null, 2);

  const requestExample = (method, path, operation, pathItem) => {
    let target = path.replace("{client_id}", sampleUUID);
    const parameters = [...(pathItem.parameters || []), ...(operation.parameters || [])].map(resolve);
    const query = parameters.filter(parameter => parameter.in === "query").map(parameter => `${parameter.name}=${parameter.example ?? parameter.schema?.default ?? sample(parameter.schema)}`);
    if (query.length) target += `?${query.join("&")}`;
    const lines = [`${method.toUpperCase()} ${target} HTTP/1.1`, "Host: vpn-admin.example.com"];
    if (path !== "/healthz") lines.push("Authorization: Bearer ovpn_v1.<uuid>.<secret>");
    parameters.filter(parameter => parameter.in === "header").forEach(parameter => lines.push(`${parameter.name}: ${parameter.example || '"<current-digest>"'}`));
    const body = operation.requestBody && resolve(operation.requestBody).content?.["application/json"];
    if (body) {
      lines.push("Content-Type: application/json", "", pretty(mediaExample(body)));
    }
    return lines.join("\n");
  };

  const appendCode = (parent, label, value, className = "code-block") => {
    parent.append(element("div", "code-label", label));
    parent.append(element("pre", className, value));
  };

  const renderParameters = (pane, operation, pathItem) => {
    const parameters = [...(pathItem.parameters || []), ...(operation.parameters || [])].map(resolve);
    if (!parameters.length) return;
    const list = element("dl", "meta");
    parameters.forEach(parameter => {
      list.append(element("dt", "", `${parameter.in}: ${parameter.name}`));
      list.append(element("dd", "", `${parameter.required ? "必填" : "可选"} · ${schemaType(parameter.schema)} · ${parameter.description || ""}`));
    });
    pane.append(list);
  };

  const renderOperation = (method, path, pathItem, operation) => {
    const article = element("article", "operation");
    article.id = operation.operationId;
    article.dataset.search = `${method} ${path} ${operation.summary} ${(operation.tags || []).join(" ")}`.toLowerCase();
    const heading = element("div", "operation-heading");
    heading.append(element("span", `method method-${method}`, method.toUpperCase()));
    heading.append(element("h2", "", path));
    article.append(heading, element("p", "operation-summary", `${operation.summary}. ${operation.description || ""}`));

    const grid = element("div", "contract-grid");
    const requestPane = element("section", "contract-pane");
    requestPane.append(element("h3", "", "请求 Request"));
    renderParameters(requestPane, operation, pathItem);
    appendCode(requestPane, "完整 HTTP 请求", requestExample(method, path, operation, pathItem));
    const requestBody = operation.requestBody && resolve(operation.requestBody).content?.["application/json"];
    if (requestBody?.schema) appendCode(requestPane, "请求字段", schemaLines(requestBody.schema).join("\n"), "schema");
    else requestPane.append(element("div", "empty", "该接口不接受请求体。"));

    const responsePane = element("section", "contract-pane");
    responsePane.append(element("h3", "", "返回 Response"));
    Object.entries(operation.responses || {}).forEach(([status, rawResponse]) => {
      const response = resolve(rawResponse);
      const block = element("div", "response");
      const title = element("div", "response-title");
      title.append(element("span", Number(status) >= 400 ? "status status-error" : "status", `${status} ${statusText[status] || ""}`));
      title.append(element("span", "", response.description || ""));
      block.append(title);
      const entries = Object.entries(response.content || {});
      if (!entries.length) block.append(element("div", "empty", "无响应体。"));
      entries.forEach(([contentType, media]) => {
        appendCode(block, `返回示例 · ${contentType}`, pretty(mediaExample(media)));
        if (media.schema) appendCode(block, "返回字段", schemaLines(media.schema).join("\n"), "schema");
      });
      responsePane.append(block);
    });
    grid.append(requestPane, responsePane);
    article.append(grid);
    return article;
  };

  const render = spec => {
    documentRoot = spec;
    const operations = document.getElementById("operations");
    const navigation = document.getElementById("navigation");
    const groups = new Map();
    let count = 0;
    Object.entries(spec.paths).forEach(([path, pathItem]) => {
      methods.forEach(method => {
        const operation = pathItem[method];
        if (!operation) return;
        count += 1;
        operations.append(renderOperation(method, path, pathItem, operation));
        const tag = (operation.tags || ["Other"])[0];
        if (!groups.has(tag)) groups.set(tag, []);
        groups.get(tag).push({method, path, operation});
      });
    });
    groups.forEach((items, name) => {
      const group = element("section", "nav-group");
      group.append(element("h2", "", name));
      items.forEach(({method, path, operation}) => {
        const link = element("a", "nav-link");
        link.href = `#${operation.operationId}`;
        link.dataset.operation = operation.operationId;
        link.append(element("span", `nav-method nav-method-${method}`, method.toUpperCase()), element("span", "nav-path", path));
        group.append(link);
      });
      navigation.append(group);
    });
    document.getElementById("operation-count").textContent = `${count} operations`;
    document.getElementById("loading").remove();
  };

  const filter = event => {
    const query = event.target.value.trim().toLowerCase();
    document.querySelectorAll(".operation").forEach(operation => {
      const visible = !query || operation.dataset.search.includes(query);
      operation.hidden = !visible;
      const link = document.querySelector(`[data-operation="${operation.id}"]`);
      if (link) link.hidden = !visible;
    });
  };

  document.getElementById("search").addEventListener("input", filter);
  fetch("/docs/openapi.json", {headers: {Accept: "application/json"}})
    .then(response => {
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      return response.json();
    })
    .then(render)
    .catch(error => {
      const loading = document.getElementById("loading");
      loading.className = "error-message";
      loading.textContent = `接口定义加载失败：${error.message}`;
    });
})();
