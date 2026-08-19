(() => {
  "use strict";

  const methods = ["get", "post", "put", "patch", "delete"];
  const sampleUUID = "c0d4f871-6ea6-42b3-9e7b-f00cc1ec354e";
  const translations = window.apiDocsI18n;
  const languageStorageKey = "ovpn-api-docs-language";
  const browserLanguage = (navigator.language || "en").toLowerCase().startsWith("zh") ? "zh" : "en";
  let currentLanguage = browserLanguage;
  let documentRoot;

  try {
    const savedLanguage = localStorage.getItem(languageStorageKey);
    if (savedLanguage === "zh" || savedLanguage === "en") currentLanguage = savedLanguage;
  } catch (_) {
    // Browser privacy settings may disable local storage; language switching still works for this page.
  }

  const text = key => translations.ui[currentLanguage][key];
  const translate = value => currentLanguage === "zh" && value ? (translations.zh[value] || value) : (value || "");
  const schemaDescription = input => {
    const schema = resolve(input);
    return currentLanguage === "zh"
      ? (schema["x-description-zh"] || translate(schema.description))
      : (schema.description || "");
  };

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
      return variants.flatMap((variant, index) => ["  ".repeat(depth) + `${text("variant")} ${index + 1}:`, ...schemaLines(variant, depth + 1, nextSeen)]);
    }
    if (schema.type === "array") return ["  ".repeat(depth) + text("arrayOf"), ...schemaLines(schema.items, depth + 1, nextSeen)];
    if (schema.type !== "object" && !schema.properties) return ["  ".repeat(depth) + schemaType(schema)];
    const required = new Set(schema.required || []);
    const lines = [];
    Object.entries(schema.properties || {}).forEach(([name, property]) => {
      const resolved = resolve(property);
      const marker = required.has(name) ? text("required") : text("optional");
      const descriptionText = schemaDescription(property);
      const description = descriptionText ? ` - ${descriptionText}` : "";
      lines.push(`${"  ".repeat(depth)}${name}: ${schemaType(property)} [${marker}]${description}`);
      if ((resolved.type === "object" || resolved.properties || resolved.type === "array" || resolved.oneOf) && depth < 3) {
        lines.push(...schemaLines(property, depth + 1, nextSeen));
      }
    });
    return lines.length ? lines : ["  ".repeat(depth) + text("object")];
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
    if (schema.format) constraints.push(`${text("constraintFormat")}: ${schema.format}`);
    if (schema.const !== undefined) constraints.push(`${text("constant")}: ${JSON.stringify(schema.const)}`);
    if (schema.enum) constraints.push(`${text("values")}: ${schema.enum.map(value => JSON.stringify(value)).join(" | ")}`);
    if (schema.pattern) constraints.push(`${text("constraintFormat")}: ${schema.pattern}`);
    if (schema.minimum !== undefined) constraints.push(`${text("minimum")}: ${schema.minimum}`);
    if (schema.maximum !== undefined) constraints.push(`${text("maximum")}: ${schema.maximum}`);
    if (schema.default !== undefined) constraints.push(`${text("defaultValue")}: ${JSON.stringify(schema.default)}`);
    return constraints.join(currentLanguage === "zh" ? "；" : "; ");
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
        description: schemaDescription(property) || text("requestBodyField")
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
        description: text("authDescription")
      });
    }
    if (body) {
      groups.header.push({
        name: "Content-Type",
        location: "header",
        type: "string",
        required: true,
        format: `${text("constant")}: application/json`,
        example: "application/json",
        description: text("contentTypeDescription")
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
        description: translate(parameter.description) || text("requestParameter")
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
      if (!row.format || !row.format.includes(example)) details.push(`${text("example")}: ${example}`);
    }
    return details.join(currentLanguage === "zh" ? "；" : "; ") || "-";
  };

  const renderParameterTable = (pane, title, rows) => {
    if (!rows.length) return;
    pane.append(element("h4", "parameter-title", title));
    const wrapper = element("div", "parameter-table-wrap");
    const table = element("table", "parameter-table");
    const head = element("thead");
    const header = element("tr");
    text("tableHeaders").forEach(label => header.append(element("th", "", label)));
    head.append(header);
    const body = element("tbody");
    rows.forEach(row => {
      const tr = element("tr");
      tr.append(
        tableCell(row.name, true),
        tableCell(row.location, true),
        tableCell(row.type, true),
        tableCell(row.required ? text("yes") : text("no")),
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
    renderParameterTable(pane, text("headerParameters"), groups.header);
    renderParameterTable(pane, text("pathParameters"), groups.path);
    renderParameterTable(pane, text("queryParameters"), groups.query);
    renderParameterTable(pane, text("bodyFields"), groups.body);
    if (!Object.values(groups).some(rows => rows.length)) {
      pane.append(element("div", "empty", text("noRequest")));
    }
  };

  const renderContractTabs = (operationId, requestPane, responsePane) => {
    const tabs = element("div", "contract-tabs");
    tabs.setAttribute("role", "tablist");
    tabs.setAttribute("aria-label", text("tabsLabel"));
    const panes = [requestPane, responsePane];
    const activate = selected => {
      Array.from(tabs.children).forEach((tab, index) => {
        const active = index === selected;
        tab.setAttribute("aria-selected", String(active));
        tab.tabIndex = active ? 0 : -1;
        panes[index].hidden = !active;
      });
    };
    [text("requestTab"), text("responseTab")].forEach((label, index) => {
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
    article.dataset.search = `${method} ${path} ${operation.summary} ${translate(operation.summary)} ${operation.description || ""} ${translate(operation.description)} ${(operation.tags || []).join(" ")}`.toLowerCase();
    const heading = element("div", "operation-heading");
    heading.append(element("span", `method method-${method}`, method.toUpperCase()));
    heading.append(element("h2", "", path));
    const summary = translate(operation.summary);
    const description = translate(operation.description);
    article.append(heading, element("p", "operation-summary", description ? `${summary}${currentLanguage === "zh" ? "。" : ". "}${description}` : summary));

    const grid = element("div", "contract-stack");
    const requestPane = element("section", "contract-pane");
    requestPane.id = `${operation.operationId}-request`;
    renderParameters(requestPane, operation, pathItem);
    appendCode(requestPane, text("requestExample"), requestExample(method, path, operation, pathItem));

    const responsePane = element("section", "contract-pane");
    responsePane.id = `${operation.operationId}-response`;
    Object.entries(operation.responses || {}).forEach(([status, rawResponse]) => {
      const response = resolve(rawResponse);
      const block = element("div", "response");
      const title = element("div", "response-title");
      title.append(element("span", Number(status) >= 400 ? "status status-error" : "status", `${status} ${text("status")[status] || ""}`));
      title.append(element("span", "", translate(response.description)));
      block.append(title);
      const entries = Object.entries(response.content || {});
      if (!entries.length) block.append(element("div", "empty", text("noResponseBody")));
      entries.forEach(([contentType, media]) => {
        appendCode(block, `${text("responseExample")} · ${contentType}`, pretty(mediaExample(media)));
        if (media.schema) appendCode(block, text("responseFields"), schemaLines(media.schema).join("\n"), "schema");
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
    operations.replaceChildren();
    navigation.replaceChildren();
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
      group.append(element("h2", "", text("tags")[name] || name));
      items.forEach(({method, path, operation}) => {
        const link = element("a", "nav-link");
        link.href = `#${operation.operationId}`;
        link.dataset.operation = operation.operationId;
        link.append(element("span", `nav-method nav-method-${method}`, method.toUpperCase()), element("span", "nav-path", path));
        group.append(link);
      });
      navigation.append(group);
    });
    document.getElementById("operation-count").textContent = `${count} ${text("operations")}`;
    document.getElementById("loading")?.remove();
    applyFilter(document.getElementById("search").value.trim().toLowerCase());
    if (location.hash) document.getElementById(decodeURIComponent(location.hash.slice(1)))?.scrollIntoView();
  };

  const applyFilter = query => {
    document.querySelectorAll(".operation").forEach(operation => {
      const visible = !query || operation.dataset.search.includes(query);
      operation.hidden = !visible;
      const link = document.querySelector(`[data-operation="${operation.id}"]`);
      if (link) link.hidden = !visible;
    });
  };

  const applyStaticLanguage = () => {
    document.documentElement.lang = currentLanguage === "zh" ? "zh-CN" : "en";
    document.title = text("documentTitle");
    document.getElementById("brand").setAttribute("aria-label", text("brandLabel"));
    document.getElementById("search-label").textContent = text("searchLabel");
    document.getElementById("search").placeholder = text("searchPlaceholder");
    document.getElementById("sidebar").setAttribute("aria-label", text("navigationLabel"));
    document.getElementById("interfaces-label").textContent = text("interfaces");
    document.getElementById("page-title").textContent = text("title");
    document.getElementById("intro-before").textContent = text("introBefore");
    document.getElementById("intro-after").textContent = text("introAfter");
    document.querySelectorAll("#configuration-table th").forEach((header, index) => {
      header.textContent = text("configurationHeaders")[index];
    });
    document.getElementById("listen-default").textContent = text("emptyValue");
    document.getElementById("listen-values").textContent = text("listenValues");
    document.getElementById("listen-description").textContent = text("listenDescription");
    document.getElementById("cors-default").textContent = text("emptyValue");
    document.getElementById("cors-values").textContent = text("corsValues");
    document.getElementById("cors-description").textContent = text("corsDescription");
    document.getElementById("api-key-title").textContent = text("apiKeyTitle");
    document.getElementById("api-key-convention-before").textContent = text("apiKeyConventionBefore");
    document.getElementById("api-key-convention-after").textContent = text("apiKeyConventionAfter");
    document.querySelectorAll("#api-key-table th").forEach((header, index) => {
      header.textContent = text("apiKeyHeaders")[index];
    });
    document.getElementById("api-key-create-description").textContent = text("apiKeyCreateDescription");
    document.getElementById("api-key-output-description").textContent = text("apiKeyOutputDescription");
    document.getElementById("api-key-list-description").textContent = text("apiKeyListDescription");
    document.getElementById("api-key-delete-description").textContent = text("apiKeyDeleteDescription");
    document.getElementById("base-path-label").textContent = text("basePath");
    document.getElementById("format-label").textContent = text("format");
    const loading = document.getElementById("loading");
    if (loading) loading.textContent = text("loading");
    document.querySelectorAll("[data-language]").forEach(button => {
      button.setAttribute("aria-pressed", String(button.dataset.language === currentLanguage));
    });
  };

  const setLanguage = (language, persist) => {
    if (language !== "zh" && language !== "en") return;
    currentLanguage = language;
    if (persist) {
      try {
        localStorage.setItem(languageStorageKey, language);
      } catch (_) {
        // Keep the in-memory selection when storage is unavailable.
      }
    }
    applyStaticLanguage();
    if (documentRoot) render(documentRoot);
  };

  applyStaticLanguage();
  document.getElementById("search").addEventListener("input", event => applyFilter(event.target.value.trim().toLowerCase()));
  document.getElementById("language-switch").addEventListener("click", event => {
    const language = event.target.closest("[data-language]")?.dataset.language;
    if (language) setLanguage(language, true);
  });
  fetch("/docs/openapi.json", {headers: {Accept: "application/json"}})
    .then(response => {
      if (!response.ok) throw new Error(`HTTP ${response.status}`);
      return response.json();
    })
    .then(render)
    .catch(error => {
      const loading = document.getElementById("loading");
      loading.className = "error-message";
      loading.textContent = `${text("loadError")}${error.message}`;
    });
})();
