/** Device-local favorites with optional, per-user operation-based synchronization. */
(function () {
	"use strict";
	var user = document.querySelector('meta[name="user-id"]');
	if (!user) return;
	var prefix = "treckrr:favorites:v1:" + user.content + ":";
	var kinds = ["neighbor", "gespann", "task"];
	function canonical(kind) { return { neighbors: "neighbor", gespanne: "gespann", tasks: "task" }[kind] || kind; }
	var refreshers = [], syncing = false, resync = false, enabled = false;
	try { enabled = localStorage.getItem(prefix + "sync") === "true"; } catch (_) { /* Storage is optional. */ }
	var setting = document.querySelector("[data-favorite-sync]");
	var status = document.querySelector("[data-favorite-sync-status]");
	if (setting) setting.disabled = true;
	function notify(message) { if (status) status.textContent = message; }
	function pending() {
		try { var value = JSON.parse(localStorage.getItem(prefix + "pending") || "[]"); return Array.isArray(value) ? value.slice(-48) : []; } catch (_) { return []; }
	}
	function savePending(values) { try { localStorage.setItem(prefix + "pending", JSON.stringify(values)); } catch (_) { notify("Lokaler Speicher nicht verfügbar. Änderungen können offline nicht gesichert werden."); } }
	function refresh() { refreshers.forEach(function (fn) { fn(); }); }
	function overlay(state) {
		enabled = state.enabled === true;
		try { localStorage.setItem(prefix + "sync", String(enabled)); } catch (_) { /* Storage is optional. */ }
		if (setting) setting.checked = enabled;
		if (!enabled) {
			savePending([]);
			try { localStorage.removeItem(prefix + "imported"); } catch (_) { /* Optional storage. */ }
			notify("Nur auf diesem Gerät gespeichert."); return;
		}
		// Import existing device favorites once, never silently replace them when
		// synchronization was enabled on a different device.
		try {
			if (localStorage.getItem(prefix + "imported") !== "true") {
				var queued = pending();
				kinds.forEach(function (kind) { read(kind).forEach(function (value) {
					if (!queued.some(function (c) { return c.kind === kind && c.value === value; })) queued.push({ kind: kind, value: value, active: true, token: String(Date.now()) + Math.random() });
				}); });
				savePending(queued); localStorage.setItem(prefix + "imported", "true");
			}
		} catch (_) { /* Optional storage. */ }
		kinds.forEach(function (kind) {
			var values = Array.isArray(state.items[kind]) ? state.items[kind].filter(function (v) { return typeof v === "string"; }) : [];
			pending().filter(function (c) { return c.kind === kind; }).forEach(function (c) {
				values = values.filter(function (v) { return v !== c.value; }); if (c.active) values.unshift(c.value);
			});
			write(kind, values);
		});
		refresh(); notify(pending().length ? "Lokale Änderungen warten auf Abgleich." : "Favoriten abgeglichen.");
	}
	function request(payload) {
		var token = document.querySelector('meta[name="csrf-token"]');
		var controller = new AbortController();
		var timeout = setTimeout(function () { controller.abort(); }, 10000);
		return fetch("/account/favorites", {
			method: payload ? "POST" : "GET", credentials: "same-origin", cache: "no-store",
			signal: controller.signal,
			headers: payload ? { "Content-Type": "application/json", "X-CSRF-Token": token ? token.content : "", "X-Preference-User": user.content } : {},
			body: payload ? JSON.stringify(payload) : undefined
		}).then(function (response) { if (!response.ok || !/application\/json/.test(response.headers.get("Content-Type") || "")) throw new Error("sync"); return response.json(); }).then(function (state) { if (String(state.user_id) !== user.content || !state.items) throw new Error("session changed"); return state; }).finally(function () { clearTimeout(timeout); });
	}
	function sync() {
		if (syncing) { resync = true; return; }
		if (!navigator.onLine) { if (setting) setting.disabled = false; return; }
		syncing = true;
		resync = false;
		var success = false;
		if (setting) setting.disabled = true;
		request().then(function (state) {
			overlay(state);
			var changes = pending().slice(0, 24);
			if (!enabled || !changes.length) return;
			return request({ changes: changes.map(function (c) { return { kind: c.kind, value: c.value, active: c.active }; }) }).then(function (next) {
				var sent = new Set(changes.map(function (c) { return c.token; }));
				savePending(pending().filter(function (c) { return !sent.has(c.token); })); overlay(next);
			});
		}).then(function () { success = true; }).catch(function () { notify("Abgleich nicht erreichbar. Lokale Favoriten bleiben erhalten."); }).finally(function () {
			syncing = false;
			if (setting) setting.disabled = false;
			if (success && (resync || (enabled && pending().length))) setTimeout(sync, 0);
		});
	}

	function read(kind) {
		kind = canonical(kind);
		try {
			var value = JSON.parse(localStorage.getItem(prefix + kind) || "[]");
			return Array.isArray(value) ? value.filter(function (item) { return typeof item === "string"; }).slice(0, 8) : [];
		} catch (_) {
			return [];
		}
	}

	function write(kind, values) {
		kind = canonical(kind);
		try { localStorage.setItem(prefix + kind, JSON.stringify(values.slice(0, 8))); } catch (_) { /* Storage is optional. */ }
	}

	function toggle(kind, value) {
		kind = canonical(kind);
		if (!value) return false;
		var values = read(kind);
		var index = values.indexOf(value);
		if (index >= 0) values.splice(index, 1);
		else values.unshift(value);
		write(kind, values);
		if (enabled) {
			var queued = pending().filter(function (c) { return c.kind !== kind || c.value !== value; });
			queued.push({ kind: kind, value: value, active: index < 0, token: String(Date.now()) + Math.random() });
			savePending(queued); sync();
		}
		return index < 0;
	}

	function updateButton(button, active) {
		button.classList.toggle("is-favorite", active);
		button.setAttribute("aria-pressed", active ? "true" : "false");
		var label = active ? "Favorit entfernen" : "Als Favorit merken";
		button.setAttribute("title", label);
		button.setAttribute("aria-label", label);
		var text = button.querySelector("[data-favorite-label]");
		if (text) text.textContent = active ? "Favorit" : "Merken";
	}

	function sortItems(list, kind) {
		var favorites = read(kind);
		var rows = Array.prototype.slice.call(list.querySelectorAll(":scope > [data-favorite-id]"));
		rows.sort(function (a, b) {
			var ai = favorites.indexOf(a.getAttribute("data-favorite-id"));
			var bi = favorites.indexOf(b.getAttribute("data-favorite-id"));
			if (ai < 0) ai = Number.MAX_SAFE_INTEGER;
			if (bi < 0) bi = Number.MAX_SAFE_INTEGER;
			return ai - bi;
		});
		rows.forEach(function (row) { list.appendChild(row); });
	}

	document.querySelectorAll("[data-favorite-list]").forEach(function (list) {
		var kind = list.getAttribute("data-favorite-list");
		sortItems(list, kind);
		refreshers.push(function () { sortItems(list, kind); list.querySelectorAll("[data-favorite-id]").forEach(function (row) { var b = row.querySelector("[data-favorite-toggle]"); if (b) updateButton(b, read(kind).indexOf(row.dataset.favoriteId) >= 0); }); });
		list.querySelectorAll("[data-favorite-id]").forEach(function (row) {
			var id = row.getAttribute("data-favorite-id");
			var button = row.querySelector("[data-favorite-toggle]");
			if (!button) return;
			updateButton(button, read(kind).indexOf(id) >= 0);
			button.addEventListener("click", function () {
				updateButton(button, toggle(kind, id));
				sortItems(list, kind);
			});
		});
	});

	document.querySelectorAll("[data-favorite-select]").forEach(function (select) {
		var kind = select.getAttribute("data-favorite-select");
		var button = document.querySelector('[data-favorite-select-toggle="' + kind + '"]');
		function active() { return read(kind).indexOf(select.value) >= 0; }
		function sortOptions() {
			var selected = select.value;
			var favorites = read(kind);
			var options = Array.prototype.slice.call(select.options).filter(function (option) { return option.value; });
			options.sort(function (a, b) {
				var ai = favorites.indexOf(a.value), bi = favorites.indexOf(b.value);
				if (ai < 0) ai = Number.MAX_SAFE_INTEGER;
				if (bi < 0) bi = Number.MAX_SAFE_INTEGER;
				return ai - bi;
			});
			options.forEach(function (option) { select.appendChild(option); });
			select.value = selected;
		}
		sortOptions();
		refreshers.push(function () { sortOptions(); if (button) updateButton(button, active()); });
		if (!button) return;
		updateButton(button, active());
		button.disabled = !select.value;
		select.addEventListener("change", function () {
			button.disabled = !select.value;
			updateButton(button, active());
		});
		button.addEventListener("click", function () {
			if (!select.value) return;
			updateButton(button, toggle(kind, select.value));
			sortOptions();
		});
	});

	document.querySelectorAll("[data-favorite-text]").forEach(function (box) {
		var kind = box.getAttribute("data-favorite-text");
		var input = box.querySelector("[data-favorite-text-input]");
		var button = box.querySelector("[data-favorite-text-toggle]");
		var list = box.querySelector("[data-favorite-text-list]");
		if (!input || !button || !list) return;
		function value() { return input.value.trim(); }
		function render() {
			var current = value();
			var values = read(kind);
			list.replaceChildren();
			values.forEach(function (favorite) {
				var choice = document.createElement("button");
				choice.type = "button";
				choice.className = "favorite-chip";
				choice.textContent = favorite;
				choice.addEventListener("click", function () {
					input.value = favorite;
					input.dispatchEvent(new Event("input", { bubbles: true }));
					input.focus();
					render();
				});
				list.appendChild(choice);
			});
			button.disabled = !current;
			updateButton(button, values.indexOf(current) >= 0);
		}
		input.addEventListener("input", render);
		button.addEventListener("click", function () { if (value()) toggle(kind, value()); render(); });
		render();
		refreshers.push(render);
	});
	if (setting) setting.addEventListener("change", function () {
		if (syncing) { setting.checked = enabled; return; }
		var on = setting.checked, changes = [];
		if (on) kinds.forEach(function (kind) { read(kind).forEach(function (value) { changes.push({ kind: kind, value: value, active: true }); }); });
		setting.disabled = true;
		syncing = true;
		request({ enabled: on, changes: changes }).then(overlay).catch(function () { setting.checked = enabled; notify("Einstellung konnte nicht gespeichert werden. Bitte erneut versuchen."); }).finally(function () { syncing = false; setting.disabled = false; if (enabled && pending().length) sync(); });
	});
	window.addEventListener("online", sync);
	window.addEventListener("focus", sync);
	window.addEventListener("storage", function (event) { if (event.key && event.key.indexOf(prefix) === 0) refresh(); });
	sync();
})();
