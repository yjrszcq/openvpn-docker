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

  const requestType = input => {
    const schema = resolve(input);
    if (schema.oneOf) return schema.oneOf.map(requestType).join(" | ");
    if (schema.anyOf) return schema.anyOf.map(requestType).join(" | ");
    if (Array.isArray(schema.type)) return schema.type.join(" | ");
    if (schema.type === "array") return `${requestType(schema.items)}[]`;
    if (schema.type === "object" || schema.properties) return "object";
    return schema.type || "any";
  };

  const requestFormat = input => {
    const schema = resolve(input);
    const constraints = [];
    if (schema.format) constraints.push(`format: ${schema.format}`);
    if (schema.const !== undefined) constraints.push(`固定值: ${JSON.stringify(schema.const)}`);
    if (schema.enum) constraints.push(`可选值: ${schema.enum.map(value => JSON.stringify(value)).join(" | ")}`);
    if (schema.pattern) constraints.push(`格式: ${schema.pattern}`);
    if (schema.minimum !== undefined) constraints.push(`最小值: ${schema.minimum}`);
    if (schema.maximum !== undefined) constraints.push(`最大值: ${schema.maximum}`);
    if (schema.default !== undefined) constraints.push(`默认值: ${JSON.stringify(schema.default)}`);
    return constraints.join("；");
  };

  const requiresAuthorization = operation => {
    const security = operation.security === undefined ? documentRoot.security : operation.security;
    return Array.isArray(security) && security.length > 0;
  };

  const bodyRows = (input, example, prefix = "", parentRequired = true, depth = 0) => {
    if (depth > 6) return [];
    const schema = resolve(input);
    const required = new Set(schema.required || []);
    const rows = [];
    Object.entries(schema.properties || {}).forEach(([name, property]) => {
      const resolved = resolve(property);
      const field = prefix ? `${prefix}.${name}` : name;
      const fieldRequired = parentRequired && required.has(name);
      const fieldExample = example && typeof example === "object" ? example[name] : undefined;
      const expandable = resolved.type === "object" || resolved.properties;
      rows.push({
        name: field,
        location: "body",
        type: requestType(property),
        required: fieldRequired,
        format: requestFormat(property),
        example: expandable ? undefined : (fieldExample !== undefined ? fieldExample : sample(property)),
        description: resolved.description || "JSON 请求体字段。"
      });
      if (expandable) rows.push(...bodyRows(property, fieldExample, field, fieldRequired, depth + 1));
      if (resolved.type === "array") {
        const items = resolve(resolved.items);
        if (items.type === "object" || items.properties) {
          rows.push(...bodyRows(resolved.items, Array.isArray(fieldExample) ? fieldExample[0] : undefined, `${field}[]`, fieldRequired, depth + 1));
        }
      }
    });
    return rows;
  };

  const requestRows = (operation, pathItem) => {
    const groups = {header: [], path: [], query: [], body: []};
    const body = operation.requestBody && resolve(operation.requestBody).content?.["application/json"];
    if (requiresAuthorization(operation)) {
      groups.header.push({
        name: "Authorization",
        location: "header",
        type: "string",
        required: true,
        format: "Bearer ovpn_v1.<uuid>.<secret>",
        example: "Bearer ovpn_v1.<uuid>.<secret>",
        description: "API 身份认证凭据。Bearer 后填写服务生成的 API Key。"
      });
    }
    if (body) {
      groups.header.push({
        name: "Content-Type",
        location: "header",
        type: "string",
        required: true,
        format: "固定值: application/json",
        example: "application/json",
        description: "声明请求体使用 JSON 格式。"
      });
    }
    [...(pathItem.parameters || []), ...(operation.parameters || [])].map(resolve).forEach(parameter => {
      if (!groups[parameter.in]) return;
      groups[parameter.in].push({
        name: parameter.name,
        location: parameter.in,
        type: requestType(parameter.schema),
        required: Boolean(parameter.required),
        format: requestFormat(parameter.schema),
        example: parameter.example ?? parameter.schema?.example ?? parameter.schema?.default ?? sample(parameter.schema),
        description: parameter.description || "请求参数。"
      });
    });
    if (body?.schema) {
      const requestBody = resolve(operation.requestBody);
      groups.body.push(...bodyRows(body.schema, mediaExample(body), "", Boolean(requestBody.required)));
    }
    return groups;
  };

  const requestExample = (method, path, operation, pathItem) => {
    let target = path.replace("{client_id}", sampleUUID);
    const parameters = [...(pathItem.parameters || []), ...(operation.parameters || [])].map(resolve);
    const query = parameters.filter(parameter => parameter.in === "query").map(parameter => `${parameter.name}=${parameter.example ?? parameter.schema?.default ?? sample(parameter.schema)}`);
    if (query.length) target += `?${query.join("&")}`;
    const lines = [`${method.toUpperCase()} ${target} HTTP/1.1`];
    if (requiresAuthorization(operation)) lines.push("Authorization: Bearer ovpn_v1.<uuid>.<secret>");
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

  const tableCell = (value, code = false) => {
    const cell = element("td");
    if (code && value !== "") cell.append(element("code", "", String(value)));
    else cell.textContent = value === undefined || value === "" ? "-" : String(value);
    return cell;
  };

  const requestDetails = row => {
    const details = [];
    if (row.format) details.push(row.format);
    if (row.example !== undefined) {
      const example = typeof row.example === "string" ? row.example : JSON.stringify(row.example);
      if (!row.format || !row.format.includes(example)) details.push(`示例: ${example}`);
    }
    return details.join("；") || "-";
  };

  const renderParameterTable = (pane, title, rows) => {
    if (!rows.length) return;
    pane.append(element("h4", "parameter-title", title));
    const wrapper = element("div", "parameter-table-wrap");
    const table = element("table", "parameter-table");
    const head = element("thead");
    const header = element("tr");
    ["字段", "位置", "类型", "必填", "格式 / 示例 / 约束", "用途说明"].forEach(label => header.append(element("th", "", label)));
    head.append(header);
    const body = element("tbody");
    rows.forEach(row => {
      const tr = element("tr");
      tr.append(
        tableCell(row.name, true),
        tableCell(row.location, true),
        tableCell(row.type, true),
        tableCell(row.required ? "是" : "否"),
        tableCell(requestDetails(row), true),
        tableCell(row.description)
      );
      body.append(tr);
    });
    table.append(head, body);
    wrapper.append(table);
    pane.append(wrapper);
  };

  const renderParameters = (pane, operation, pathItem) => {
    const groups = requestRows(operation, pathItem);
    renderParameterTable(pane, "Header 参数", groups.header);
    renderParameterTable(pane, "Path 参数", groups.path);
    renderParameterTable(pane, "Query 参数", groups.query);
    renderParameterTable(pane, "JSON Body 字段", groups.body);
    if (!Object.values(groups).some(rows => rows.length)) {
      pane.append(element("div", "empty", "该接口没有请求参数，也不接受请求体。"));
    }
  };

  const renderContractTabs = (operationId, requestPane, responsePane) => {
    const tabs = element("div", "contract-tabs");
    tabs.setAttribute("role", "tablist");
    tabs.setAttribute("aria-label", "请求和返回内容");
    const panes = [requestPane, responsePane];
    const activate = selected => {
      Array.from(tabs.children).forEach((tab, index) => {
        const active = index === selected;
        tab.setAttribute("aria-selected", String(active));
        tab.tabIndex = active ? 0 : -1;
        panes[index].hidden = !active;
      });
    };
    ["请求 Request", "返回 Response"].forEach((label, index) => {
      const tab = element("button", "contract-tab", label);
      tab.type = "button";
      tab.id = `${operationId}-tab-${index}`;
      tab.setAttribute("role", "tab");
      tab.setAttribute("aria-controls", panes[index].id);
      tab.addEventListener("click", () => activate(index));
      tab.addEventListener("keydown", event => {
        let next = index;
        if (event.key === "ArrowLeft" || event.key === "ArrowUp") next = (index + 1) % 2;
        else if (event.key === "ArrowRight" || event.key === "ArrowDown") next = (index + 1) % 2;
        else if (event.key === "Home") next = 0;
        else if (event.key === "End") next = 1;
        else return;
        event.preventDefault();
        activate(next);
        tabs.children[next].focus();
      });
      panes[index].setAttribute("role", "tabpanel");
      panes[index].setAttribute("aria-labelledby", tab.id);
      tabs.append(tab);
    });
    activate(0);
    return tabs;
  };

  const renderOperation = (method, path, pathItem, operation) => {
    const article = element("article", "operation");
    article.id = operation.operationId;
    article.dataset.search = `${method} ${path} ${operation.summary} ${(operation.tags || []).join(" ")}`.toLowerCase();
    const heading = element("div", "operation-heading");
    heading.append(element("span", `method method-${method}`, method.toUpperCase()));
    heading.append(element("h2", "", path));
    article.append(heading, element("p", "operation-summary", `${operation.summary}. ${operation.description || ""}`));

    const grid = element("div", "contract-stack");
    const requestPane = element("section", "contract-pane");
    requestPane.id = `${operation.operationId}-request`;
    renderParameters(requestPane, operation, pathItem);
    appendCode(requestPane, "请求示例", requestExample(method, path, operation, pathItem));

    const responsePane = element("section", "contract-pane");
    responsePane.id = `${operation.operationId}-response`;
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
    article.append(renderContractTabs(operation.operationId, requestPane, responsePane), grid);
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
    if (location.hash) document.getElementById(decodeURIComponent(location.hash.slice(1)))?.scrollIntoView();
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
