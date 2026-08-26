(() => {
  "use strict";

  const state = {
    catalogs: [],
    catalog: null,
    metadata: {},
    response: null,
    pageURLs: { next: null, prev: null },
    activeView: "records",
    cqlItems: [],
    cqlActive: -1,
    cqlRange: { start: 0, end: 0 },
  };

  const $ = (id) => document.getElementById(id);
  const elements = {
    serviceStatus: $("service-status"),
    catalogCount: $("catalog-count"),
    catalogList: $("catalog-list"),
    emptyState: $("empty-state"),
    workspace: $("workspace"),
    catalogID: $("catalog-id"),
    catalogTitle: $("catalog-title"),
    catalogDescription: $("catalog-description"),
    catalogAPILink: $("catalog-api-link"),
    recordsView: $("records-view"),
    inspectorView: $("inspector-view"),
    searchForm: $("search-form"),
    cqlInput: $("query-filter"),
    cqlSuggestions: $("cql-suggestions"),
    requestError: $("request-error"),
    requestURL: $("request-url"),
    resultSummary: $("result-summary"),
    recordList: $("record-list"),
    facetResults: $("facet-results"),
    facetContext: $("facet-context"),
    previousPage: $("previous-page"),
    nextPage: $("next-page"),
    inspectorEyebrow: $("inspector-eyebrow"),
    inspectorTitle: $("inspector-title"),
    inspectorLink: $("inspector-link"),
    inspectorJSON: $("inspector-json"),
    definitionCards: $("definition-cards"),
    recordDialog: $("record-dialog"),
    dialogTitle: $("dialog-title"),
    dialogJSON: $("dialog-json"),
  };

  const endpoint = (path) => new URL(path.replace(/^\//, ""), new URL("../", window.location.href));

  async function getJSON(input) {
    const url = input instanceof URL ? input : endpoint(input);
    const response = await fetch(url, { headers: { Accept: "application/json, application/geo+json, application/schema+json, application/facets+json" } });
    let body;
    try {
      body = await response.json();
    } catch (_) {
      body = null;
    }
    if (!response.ok) {
      const message = body?.description || body?.detail || `${response.status} ${response.statusText}`;
      throw new Error(message);
    }
    return body;
  }

  function clear(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
  }

  function text(tag, value, className) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    node.textContent = value ?? "";
    return node;
  }

  function showError(error) {
    elements.requestError.textContent = error instanceof Error ? error.message : String(error);
    elements.requestError.classList.remove("hidden");
  }

  function clearError() {
    elements.requestError.textContent = "";
    elements.requestError.classList.add("hidden");
  }

  async function loadCatalogs() {
    elements.catalogCount.textContent = "Loading…";
    clear(elements.catalogList);
    try {
      const data = await getJSON("/collections?limit=100&sortby=%2Btitle");
      state.catalogs = data.collections || [];
      elements.catalogCount.textContent = `${data.numberMatched ?? state.catalogs.length} available`;
      renderCatalogs();
      elements.serviceStatus.className = "status ok";
      elements.serviceStatus.innerHTML = "<i></i> Ready";
      if (!state.catalog && state.catalogs.length === 1) selectCatalog(state.catalogs[0].id);
    } catch (error) {
      elements.catalogCount.textContent = "Could not load collections";
      elements.serviceStatus.className = "status error";
      elements.serviceStatus.innerHTML = "<i></i> Unavailable";
      const message = text("div", error.message, "notice error");
      elements.catalogList.append(message);
    }
  }

  function renderCatalogs() {
    clear(elements.catalogList);
    if (!state.catalogs.length) {
      elements.catalogList.append(text("div", "No collections yet. Seed demo data to get started.", "muted compact"));
      return;
    }
    for (const catalog of state.catalogs) {
      const button = document.createElement("button");
      button.type = "button";
      button.className = `catalog-option${state.catalog?.id === catalog.id ? " active" : ""}`;
      button.append(text("strong", catalog.title || catalog.id));
      button.append(text("span", catalog.id));
      button.addEventListener("click", () => selectCatalog(catalog.id));
      elements.catalogList.append(button);
    }
  }

  async function selectCatalog(id) {
    clearError();
    const base = `/collections/${encodeURIComponent(id)}`;
    try {
      const [catalog, queryables, sortables, facets, schema] = await Promise.all([
        getJSON(base),
        getJSON(`${base}/queryables`),
        getJSON(`${base}/sortables`),
        getJSON(`${base}/facets`),
        getJSON(`${base}/schema?type=replace`),
      ]);
      state.catalog = catalog;
      state.metadata = { catalog, queryables, sortables, facets, schema };
      renderCatalogs();
      elements.emptyState.classList.add("hidden");
      elements.workspace.classList.remove("hidden");
      elements.catalogID.textContent = catalog.id;
      elements.catalogTitle.textContent = catalog.title || catalog.id;
      elements.catalogDescription.textContent = catalog.description || "No description supplied for this collection.";
      elements.catalogAPILink.href = endpoint(base).href;
      populateSortOptions(sortables.properties || {});
      closeCQLSuggestions();
      activateView("records");
      await runSearch();
    } catch (error) {
      showError(error);
    }
  }

  function populateSortOptions(sortables) {
    const select = $("query-sort");
    clear(select);
    select.append(new Option("Catalog default", ""));
    for (const name of Object.keys(sortables).sort()) {
      const label = sortables[name].title || name;
      select.append(new Option(`${label} ↑`, `+${name}`));
      select.append(new Option(`${label} ↓`, `-${name}`));
    }
  }

  const cqlOperators = [
    { label: "=", detail: "equal to" },
    { label: "<>", detail: "not equal to" },
    { label: "<", detail: "less than" },
    { label: "<=", detail: "less than or equal to" },
    { label: ">", detail: "greater than" },
    { label: ">=", detail: "greater than or equal to" },
  ];

  function cqlContext(value, cursor, force) {
    const before = value.slice(0, cursor);
    if (insideCQLString(before)) return { items: [], start: cursor, end: cursor };
    const queryables = state.metadata.queryables?.properties || {};

    const literal = before.match(/([A-Za-z_][A-Za-z0-9_.:-]*)\s*(=|<>|<=|>=|<|>)\s*([A-Za-z]*)$/);
    if (literal && queryables[literal[1]]) {
      const fragment = literal[3];
      const items = literalSuggestions(queryables[literal[1]])
        .filter((item) => item.label.toLowerCase().startsWith(fragment.toLowerCase()));
      return { items, start: cursor - fragment.length, end: cursor };
    }

    const operator = before.match(/([A-Za-z_][A-Za-z0-9_.:-]*)\s*([<>=!]*)$/);
    if (operator && queryables[operator[1]]) {
      const fragment = operator[2];
      const type = queryables[operator[1]].type;
      if (type === "array") return { items: [], start: cursor, end: cursor };
      const allowed = type === "boolean" ? cqlOperators.slice(0, 2) : cqlOperators;
      const items = allowed
        .filter((item) => item.label.startsWith(fragment))
        .map((item) => ({ ...item, insert: `${item.label} `, kind: "operator" }));
      return { items, start: cursor - fragment.length, end: cursor };
    }

    const token = before.match(/[A-Za-z_][A-Za-z0-9_.:-]*$/);
    const fragment = token?.[0] || "";
    const prefix = fragment.toLowerCase();
    const trimmed = before.trimEnd();
    const expectsProperty = trimmed === "" || /(?:\(|\bAND|\bOR|\bNOT)$/i.test(trimmed);
    if (!fragment && !force && !expectsProperty) return { items: [], start: cursor, end: cursor };

    const items = Object.entries(queryables)
      .map(([name, definition]) => ({
        label: name,
        insert: `${name} `,
        detail: [definition.title && definition.title !== name ? definition.title : "", cqlType(definition)].filter(Boolean).join(" · "),
        kind: "property",
      }))
      .filter((item) => !prefix || item.label.toLowerCase().startsWith(prefix) || item.detail.toLowerCase().includes(prefix))
      .sort((left, right) => {
        const leftPrefix = left.label.toLowerCase().startsWith(prefix) ? 0 : 1;
        const rightPrefix = right.label.toLowerCase().startsWith(prefix) ? 0 : 1;
        return leftPrefix - rightPrefix || left.label.localeCompare(right.label);
      });

    const syntax = [
      { label: "AND", insert: "AND ", detail: "both predicates must match", kind: "keyword" },
      { label: "OR", insert: "OR ", detail: "either predicate may match", kind: "keyword" },
      { label: "NOT", insert: "NOT ", detail: "negate a predicate", kind: "keyword" },
      { label: "TRUE", insert: "TRUE", detail: "boolean literal", kind: "literal" },
      { label: "FALSE", insert: "FALSE", detail: "boolean literal", kind: "literal" },
      { label: "DATE('')", insert: "DATE('')", detail: "date literal", kind: "literal", caretBack: 2 },
      { label: "TIMESTAMP('')", insert: "TIMESTAMP('')", detail: "timestamp literal", kind: "literal", caretBack: 2 },
    ].filter((item) => prefix && item.label.toLowerCase().startsWith(prefix));

    return { items: [...items, ...syntax].slice(0, 12), start: cursor - fragment.length, end: cursor };
  }

  function literalSuggestions(definition) {
    if (definition.type === "boolean") {
      return [
        { label: "TRUE", insert: "TRUE", detail: "boolean literal", kind: "literal" },
        { label: "FALSE", insert: "FALSE", detail: "boolean literal", kind: "literal" },
      ];
    }
    if (definition.format === "date-time") {
      return [{ label: "TIMESTAMP('')", insert: "TIMESTAMP('')", detail: "RFC 3339 timestamp", kind: "literal", caretBack: 2 }];
    }
    if (definition.format === "date") {
      return [{ label: "DATE('')", insert: "DATE('')", detail: "RFC 3339 full-date", kind: "literal", caretBack: 2 }];
    }
    if (definition.type === "number" || definition.type === "integer") {
      return [{ label: "0", insert: "0", detail: `${definition.type} literal`, kind: "literal" }];
    }
    if (definition.type === "string") {
      return [{ label: "''", insert: "''", detail: "string literal", kind: "literal", caretBack: 1 }];
    }
    return [];
  }

  function cqlType(definition) {
    if (definition.type === "array") return `array<${definition.items?.type || "value"}>`;
    return definition.format || definition.type || "value";
  }

  function insideCQLString(value) {
    let quoted = false;
    for (let index = 0; index < value.length; index++) {
      if (value[index] !== "'") continue;
      if (quoted && value[index + 1] === "'") {
        index++;
      } else {
        quoted = !quoted;
      }
    }
    return quoted;
  }

  function updateCQLSuggestions(force = false) {
    if (!state.catalog) return closeCQLSuggestions();
    const cursor = elements.cqlInput.selectionStart ?? elements.cqlInput.value.length;
    const result = cqlContext(elements.cqlInput.value, cursor, force);
    state.cqlItems = result.items;
    state.cqlRange = { start: result.start, end: result.end };
    state.cqlActive = result.items.length ? 0 : -1;
    renderCQLSuggestions();
  }

  function renderCQLSuggestions() {
    clear(elements.cqlSuggestions);
    const open = state.cqlItems.length > 0;
    elements.cqlSuggestions.classList.toggle("hidden", !open);
    elements.cqlInput.setAttribute("aria-expanded", String(open));
    if (!open) {
      elements.cqlInput.removeAttribute("aria-activedescendant");
      return;
    }
    for (const [index, item] of state.cqlItems.entries()) {
      const option = document.createElement("button");
      option.type = "button";
      option.id = `cql-option-${index}`;
      option.className = `cql-option${index === state.cqlActive ? " active" : ""}`;
      option.setAttribute("role", "option");
      option.setAttribute("aria-selected", String(index === state.cqlActive));
      const code = text("span", item.label, "cql-option-code");
      code.append(text("small", item.kind, "cql-option-kind"));
      option.append(code, text("span", item.detail, "cql-option-detail"));
      option.addEventListener("mousedown", (event) => {
        event.preventDefault();
        acceptCQLSuggestion(index);
      });
      elements.cqlSuggestions.append(option);
    }
    elements.cqlInput.setAttribute("aria-activedescendant", `cql-option-${state.cqlActive}`);
  }

  function moveCQLSelection(offset) {
    if (!state.cqlItems.length) return;
    state.cqlActive = (state.cqlActive + offset + state.cqlItems.length) % state.cqlItems.length;
    renderCQLSuggestions();
    $(`cql-option-${state.cqlActive}`)?.scrollIntoView({ block: "nearest" });
  }

  function acceptCQLSuggestion(index = state.cqlActive) {
    const item = state.cqlItems[index];
    if (!item) return;
    const input = elements.cqlInput;
    input.value = input.value.slice(0, state.cqlRange.start) + item.insert + input.value.slice(state.cqlRange.end);
    const cursor = state.cqlRange.start + item.insert.length - (item.caretBack || 0);
    closeCQLSuggestions();
    input.focus();
    input.setSelectionRange(cursor, cursor);
    updateCQLSuggestions();
  }

  function closeCQLSuggestions() {
    state.cqlItems = [];
    state.cqlActive = -1;
    elements.cqlSuggestions.classList.add("hidden");
    elements.cqlInput.setAttribute("aria-expanded", "false");
    elements.cqlInput.removeAttribute("aria-activedescendant");
  }

  function buildSearchURL() {
    const url = endpoint(`/collections/${encodeURIComponent(state.catalog.id)}/items`);
    const mappings = [
      ["query-q", "q"],
      ["query-type", "type"],
      ["query-datetime", "datetime"],
      ["query-bbox", "bbox"],
      ["query-sort", "sortby"],
      ["query-limit", "limit"],
      ["query-filter", "filter"],
    ];
    for (const [id, name] of mappings) {
      const value = $(id).value.trim();
      if (value !== "" || name === "limit") url.searchParams.set(name, value);
    }
    const mode = document.querySelector('input[name="facet-mode"]:checked')?.value || "default";
    if (mode === "none") url.searchParams.set("facets", "");
    if (mode === "all") {
      const names = Object.keys(state.metadata.facets?.facets || {}).sort();
      url.searchParams.set("facets", names.join(","));
    }
    return url;
  }

  async function runSearch(url) {
    if (!state.catalog) return;
    clearError();
    const requestURL = url || buildSearchURL();
    elements.requestURL.textContent = `${requestURL.pathname}${requestURL.search}`;
    elements.resultSummary.textContent = "Loading…";
    elements.previousPage.disabled = true;
    elements.nextPage.disabled = true;
    try {
      const data = await getJSON(requestURL);
      state.response = data;
      state.pageURLs = {
        next: responseLink(data.links, "next"),
        prev: responseLink(data.links, "prev"),
      };
      renderResponse(data);
    } catch (error) {
      elements.resultSummary.textContent = "Search failed";
      showError(error);
    }
  }

  function responseLink(links, rel) {
    const href = (links || []).find((link) => link.rel === rel)?.href;
    if (!href) return null;
    const external = new URL(href, window.location.href);
    return endpoint(`${external.pathname}${external.search}`);
  }

  function renderResponse(data) {
    const matched = data.numberMatched ?? 0;
    const returned = data.numberReturned ?? (data.features || []).length;
    elements.resultSummary.textContent = `${matched.toLocaleString()} matched · ${returned.toLocaleString()} returned`;
    elements.previousPage.disabled = !state.pageURLs.prev;
    elements.nextPage.disabled = !state.pageURLs.next;
    renderRecords(data.features || []);
    renderFacets(data.facets || {});
  }

  function renderRecords(records) {
    clear(elements.recordList);
    if (!records.length) {
      const message = $("query-limit").value === "0"
        ? "Page size is zero. Counts and facets are shown without record documents."
        : "No records matched this search.";
      elements.recordList.append(text("div", message, "zero-state"));
      return;
    }
    for (const record of records) {
      const properties = record.properties || {};
      const card = document.createElement("article");
      card.className = "record-card";
      const top = document.createElement("div");
      top.className = "record-top";
      const heading = document.createElement("div");
      heading.append(text("span", properties.type || "record", "eyebrow"));
      heading.append(text("h3", properties.title || record.id || "Untitled record"));
      heading.append(text("div", record.id || "No identifier", "record-id"));
      const inspect = text("button", "Inspect JSON", "inspect-record");
      inspect.type = "button";
      inspect.addEventListener("click", () => openRecord(record));
      top.append(heading, inspect);
      card.append(top);
      if (properties.description) card.append(text("p", properties.description, "record-description"));
      const meta = document.createElement("div");
      meta.className = "record-meta";
      meta.append(tag(properties.type || "record", "type"));
      if (properties.organization) meta.append(tag(properties.organization));
      if (properties.score !== undefined) meta.append(tag(`score ${properties.score}`));
      meta.append(tag(geometryLabel(record.geometry)));
      if (record.time) meta.append(tag(timeLabel(record.time)));
      card.append(meta);
      if (Array.isArray(properties.keywords) && properties.keywords.length) {
        const tags = document.createElement("div");
        tags.className = "tag-list";
        tags.style.marginTop = "9px";
        for (const keyword of properties.keywords.slice(0, 8)) tags.append(tag(keyword));
        card.append(tags);
      }
      elements.recordList.append(card);
    }
  }

  function tag(value, extra = "") {
    return text("span", value, `tag${extra ? ` ${extra}` : ""}`);
  }

  function geometryLabel(geometry) {
    return geometry?.type ? geometry.type : "no geometry";
  }

  function timeLabel(time) {
    if (time.date) return time.date;
    if (time.timestamp) return time.timestamp.slice(0, 10);
    if (time.interval) return `${time.interval[0]} → ${time.interval[1]}`;
    return "time supplied";
  }

  function renderFacets(facets) {
    clear(elements.facetResults);
    const entries = Object.entries(facets);
    elements.facetContext.textContent = entries.length ? "before paging" : "not requested";
    if (!entries.length) {
      elements.facetResults.append(text("div", "No facets in this response.", "zero-state compact"));
      return;
    }
    for (const [name, facet] of entries) {
      const card = document.createElement("section");
      card.className = "facet-card";
      const header = document.createElement("header");
      header.append(text("h4", name), text("span", facet.type, "facet-kind"));
      card.append(header);
      const list = document.createElement("div");
      list.className = "bucket-list";
      const max = Math.max(1, ...(facet.buckets || []).map((bucket) => bucket.count || 0));
      for (const bucket of facet.buckets || []) {
        const expression = bucketExpression(name, facet, bucket);
        const row = document.createElement(expression ? "button" : "div");
        row.className = "bucket";
        if (expression) {
          row.type = "button";
          row.title = `Add ${expression} to the CQL2 filter`;
          row.addEventListener("click", () => addFilter(expression));
        }
        const fill = document.createElement("span");
        fill.className = "bucket-fill";
        fill.style.width = `${Math.max(3, ((bucket.count || 0) / max) * 100)}%`;
        row.append(fill, text("span", bucketLabel(bucket), "bucket-label"), text("strong", String(bucket.count ?? 0), "bucket-count"));
        list.append(row);
      }
      if (!(facet.buckets || []).length) list.append(text("div", "No buckets", "muted compact"));
      card.append(list);
      if (facet.more) card.append(text("p", "More buckets are available; request a larger bucket count through the API.", "muted compact"));
      elements.facetResults.append(card);
    }
  }

  function bucketLabel(bucket) {
    if (bucket.value !== undefined && bucket.value !== null) return String(bucket.value);
    return `${formatBoundary(bucket.min)} – ${formatBoundary(bucket.max)}`;
  }

  function formatBoundary(value) {
    if (typeof value === "number") return Number.isInteger(value) ? String(value) : value.toFixed(2).replace(/0+$/, "").replace(/\.$/, "");
    if (typeof value === "string" && /^\d{4}-\d{2}-\d{2}T/.test(value)) return value.slice(0, 10);
    return String(value ?? "…");
  }

  function bucketExpression(name, facet, bucket) {
    const property = facet.property;
    if (facet.type === "filter") {
      return state.metadata.facets?.facets?.[name]?.filters?.[bucket.value] || null;
    }
    if (!property) return null;
    const queryable = state.metadata.queryables?.properties?.[property];
    if (facet.type === "term") {
      if (queryable?.type === "array") return null;
      if (typeof bucket.value === "number") return `${property} = ${bucket.value}`;
      if (typeof bucket.value === "boolean") return `${property} = ${bucket.value ? "TRUE" : "FALSE"}`;
      return `${property} = '${String(bucket.value).replaceAll("'", "''")}'`;
    }
    if (facet.type === "histogram") {
      if (queryable?.format === "date-time") {
        return `${property} >= TIMESTAMP('${bucket.min}') AND ${property} < TIMESTAMP('${bucket.max}')`;
      }
      if (typeof bucket.min === "number" && typeof bucket.max === "number") {
        return `${property} >= ${bucket.min} AND ${property} < ${bucket.max}`;
      }
    }
    return null;
  }

  function addFilter(expression) {
    const input = $("query-filter");
    input.value = input.value.trim() ? `(${input.value.trim()}) AND (${expression})` : expression;
    closeCQLSuggestions();
    runSearch();
  }

  function activateView(view) {
    state.activeView = view;
    for (const button of document.querySelectorAll("#tabs button")) button.classList.toggle("active", button.dataset.view === view);
    elements.recordsView.classList.toggle("hidden", view !== "records");
    elements.inspectorView.classList.toggle("hidden", view === "records");
    if (view === "records") return;
    const config = {
      catalog: ["Collection document", "Catalog JSON", `/collections/${encodeURIComponent(state.catalog.id)}`, state.metadata.catalog],
      facets: ["Part 2", "Facet definitions", `/collections/${encodeURIComponent(state.catalog.id)}/facets`, state.metadata.facets],
      queryables: ["CQL2", "Queryable properties", `/collections/${encodeURIComponent(state.catalog.id)}/queryables`, state.metadata.queryables],
      sortables: ["Ordering", "Sortable properties", `/collections/${encodeURIComponent(state.catalog.id)}/sortables`, state.metadata.sortables],
      schema: ["Part 3", "Receivable record schema", `/collections/${encodeURIComponent(state.catalog.id)}/schema?type=replace`, state.metadata.schema],
    }[view];
    if (!config) return;
    elements.inspectorEyebrow.textContent = config[0];
    elements.inspectorTitle.textContent = config[1];
    elements.inspectorLink.href = endpoint(config[2]).href;
    elements.inspectorJSON.textContent = JSON.stringify(config[3], null, 2);
    renderDefinitionCards(view, config[3]);
  }

  function renderDefinitionCards(view, document) {
    clear(elements.definitionCards);
    const values = view === "facets" ? document?.facets : (view === "queryables" || view === "sortables" ? document?.properties : null);
    elements.definitionCards.classList.toggle("hidden", !values);
    if (!values) return;
    for (const name of Object.keys(values).sort()) {
      const definition = values[name] || {};
      const card = documentNode("article", "definition-card");
      card.append(text("h3", definition.title || name));
      if (definition.title) card.append(text("p", name));
      if (definition.description) card.append(text("p", definition.description));
      const terms = [];
      for (const key of ["type", "property", "format", "bucketType", "bucketCount", "sortedBy"]) {
        if (definition[key] !== undefined) terms.push([key, String(definition[key])]);
      }
      if (terms.length) {
        const dl = document.createElement("dl");
        for (const [key, value] of terms) dl.append(text("dt", key), text("dd", value));
        card.append(dl);
      }
      elements.definitionCards.append(card);
    }
  }

  function documentNode(tagName, className) {
    const node = document.createElement(tagName);
    node.className = className;
    return node;
  }

  function openRecord(record) {
    elements.dialogTitle.textContent = record.properties?.title || record.id || "Record";
    elements.dialogJSON.textContent = JSON.stringify(record, null, 2);
    elements.recordDialog.showModal();
  }

  function resetSearch() {
    elements.searchForm.reset();
    $("query-limit").value = "10";
    $("query-sort").value = "";
    closeCQLSuggestions();
    runSearch();
  }

  elements.searchForm.addEventListener("submit", (event) => {
    event.preventDefault();
    runSearch();
  });
  $("reset-search").addEventListener("click", resetSearch);
  $("refresh-catalogs").addEventListener("click", loadCatalogs);
  elements.cqlInput.addEventListener("input", () => updateCQLSuggestions());
  elements.cqlInput.addEventListener("focus", () => updateCQLSuggestions(true));
  elements.cqlInput.addEventListener("click", () => updateCQLSuggestions(true));
  elements.cqlInput.addEventListener("blur", () => window.setTimeout(closeCQLSuggestions, 100));
  elements.cqlInput.addEventListener("keydown", (event) => {
    if ((event.ctrlKey || event.metaKey) && event.code === "Space") {
      event.preventDefault();
      updateCQLSuggestions(true);
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (!state.cqlItems.length) updateCQLSuggestions(true);
      else moveCQLSelection(event.key === "ArrowDown" ? 1 : -1);
      return;
    }
    if ((event.key === "Enter" || event.key === "Tab") && state.cqlItems.length) {
      event.preventDefault();
      acceptCQLSuggestion();
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      closeCQLSuggestions();
    }
  });
  elements.previousPage.addEventListener("click", () => state.pageURLs.prev && runSearch(state.pageURLs.prev));
  elements.nextPage.addEventListener("click", () => state.pageURLs.next && runSearch(state.pageURLs.next));
  $("copy-url").addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(new URL(elements.requestURL.textContent, window.location.origin).href);
      $("copy-url").textContent = "Copied";
      window.setTimeout(() => { $("copy-url").textContent = "Copy"; }, 1200);
    } catch (_) {
      $("copy-url").textContent = "Copy failed";
    }
  });
  $("close-dialog").addEventListener("click", () => elements.recordDialog.close());
  elements.recordDialog.addEventListener("click", (event) => {
    if (event.target === elements.recordDialog) elements.recordDialog.close();
  });
  for (const button of document.querySelectorAll("#tabs button")) button.addEventListener("click", () => activateView(button.dataset.view));

  loadCatalogs();
})();
