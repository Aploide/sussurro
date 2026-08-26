

// ---- Bootstrap ----
document.addEventListener('DOMContentLoaded', async () => {
  initTabs();
  initFoldableSections();
  await reloadSettings();
});

let settingsLayoutFrame = 0;
let lastRequestedSettingsSize = '';

// Coalesce DOM changes so measurements happen after the browser has laid out
// the visible tab. Hidden panels report zero dimensions.
function scheduleSettingsLayout() {
  if (settingsLayoutFrame) return;
  settingsLayoutFrame = requestAnimationFrame(() => {
    settingsLayoutFrame = 0;
    sizeDictionaryColumns();
    requestSettingsSize();
  });
}

function visibleElementHeight(element) {
  if (!element || element.hidden) return 0;
  return Math.max(element.scrollHeight, element.getBoundingClientRect().height);
}

function naturalSettingsHeight() {
  const content = document.querySelector('.content');
  if (!content) return 0;

  const style = getComputedStyle(content);
  const children = Array.from(content.children).filter(child => !child.hidden);
  const gap = Number.parseFloat(style.rowGap) || 0;
  let height =
    (Number.parseFloat(style.paddingTop) || 0) +
    (Number.parseFloat(style.paddingBottom) || 0) +
    Math.max(0, children.length - 1) * gap;
  children.forEach(child => {
    height += visibleElementHeight(child);
  });

  height += visibleElementHeight(document.getElementById('restart-banner'));
  height += visibleElementHeight(document.querySelector('.statusbar'));
  return Math.ceil(height);
}

// Retry budget for a viewport that has not yet reached its real width.
const MAX_NARROW_RETRIES = 30;
let narrowLayoutRetries = 0;

// The narrowest viewport the settings layout is designed for. Must match
// settingsContentWidth in settings.go, which is the width below which controls
// overflow their rows; settings_assets_test.go pins the two together.
const MIN_SETTINGS_WIDTH = 793;

function requestSettingsSize() {
  if (typeof window.resizeSettingsWindow !== 'function') return;
  const width = Math.ceil(document.documentElement.clientWidth);
  const height = naturalSettingsHeight();
  if (!width || !height) return;

  // Ignore a viewport narrower than the layout supports, but come back for
  // another look. WebKit reports a transient sliver before the window reaches
  // its real size, and every height measured against it is wrong twice over:
  // bogus in itself, and inflated because the narrow width wraps the content.
  // Sizing from one made the window jump tall and then visibly shrink once the
  // true measurement arrived, e.g. 138x1074 followed by 794x613.
  //
  // Rescheduling matters as much as skipping: this is the only measurement the
  // page takes on open, so simply returning leaves the window stuck at its
  // startup minimum with nothing to widen it. See sussurro-bbz.
  if (width < MIN_SETTINGS_WIDTH) {
    // Bounded, because HintMin should prevent a genuinely narrow viewport and
    // an unbounded retry would spin a rAF loop for the window's lifetime.
    // A handful of frames is ample for WebKit to settle after a resize.
    if (narrowLayoutRetries < MAX_NARROW_RETRIES) {
      narrowLayoutRetries += 1;
      scheduleSettingsLayout();
    }
    return;
  }
  narrowLayoutRetries = 0;

  const size = `${width}x${height}`;
  if (size === lastRequestedSettingsSize) return;
  lastRequestedSettingsSize = size;
  window.resizeSettingsWindow(width, height);
}

// Re-fetch data and re-render in place — never calls location.reload() which
// destroys the WebKit JS context mid-execution and causes a crash.
async function reloadSettings() {
  try {
    const raw = await window.getInitialData();
    const data = JSON.parse(raw);
    render(data);
  } catch (e) {
    console.error('reloadSettings failed:', e);
  }
}

// ---- Render ----
function render(data) {
  // Apply an explicit override before building the page to avoid a flash of
  // the system palette on first open.
  renderTheme(data.theme);

  // Status bar labels
  document.getElementById('platform-label').textContent = data.platform;
  document.getElementById('version-label').textContent  = `v${data.version}`;

  // Models
  const whisperItems = data.models.filter(m => m.type === 'whisper');
  const llmItems     = data.models.filter(m => m.type === 'llm');
  renderModelList('whisper-list', whisperItems, 'whisper');
  renderModelList('llm-list',     llmItems,     'llm');

  // Hotkey
  renderHotkey(data.pushToTalkHotkey, data.toggleHotkey, data.isWayland);

  // Language
  renderLanguage(data.language);

  // Lowercase output
  renderLowercaseOutput(data.lowercaseOutput);
  renderSkipLLMCleanup(data.skipLLMCleanup);
  renderDictionary(data.dictionary || []);
  renderWorkflow(data.workflow);
  scheduleSettingsLayout();
}

// ---- Tabs ----
// The page grew past comfortable scrolling as settings were added, so
// related sections are grouped and only one group is shown at a time.
function initTabs() {
  const tabs = Array.from(document.querySelectorAll('[data-tab]'));
  const panels = Array.from(document.querySelectorAll('[data-tab-panel]'));
  if (!tabs.length) return;

  const select = name => {
    tabs.forEach(tab => {
      tab.setAttribute('aria-selected', tab.dataset.tab === name ? 'true' : 'false');
    });
    panels.forEach(panel => {
      panel.hidden = panel.dataset.tabPanel !== name;
    });
    // Scrolling is per-panel, so a new tab starts at its top rather than
    // inheriting the previous panel's scroll position.
    const content = document.querySelector('.content');
    if (content) content.scrollTop = 0;
    scheduleSettingsLayout();
  };

  tabs.forEach(tab => {
    tab.onclick = () => select(tab.dataset.tab);
  });
}

// ---- Foldable sections ----
function initFoldableSections() {
  const sections = document.querySelectorAll('.section.foldable');
  sections.forEach(section => {
    const header = section.querySelector('[data-section-toggle]');
    const body = section.querySelector('.section-body');
    if (!header || !body) return;

    header.onclick = () => {
      const collapse = !section.classList.contains('collapsed');
      section.classList.toggle('collapsed', collapse);
      body.hidden = collapse;
      header.setAttribute('aria-expanded', collapse ? 'false' : 'true');
      scheduleSettingsLayout();
    };
  });
}

// ---- Model list ----
function renderModelList(containerId, models, groupName) {
  const container = document.getElementById(containerId);
  container.replaceChildren();

  models.forEach(m => {
    const item = document.createElement('div');
    item.className = 'model-item' + (m.active ? ' active' : '');
    item.dataset.id = m.id;

    const radio = document.createElement('input');
    radio.type = 'radio';
    radio.name = groupName;
    radio.value = m.id;
    radio.checked = m.active;

    const info = document.createElement('div');
    info.className = 'model-info';
    const name = document.createElement('span');
    name.className = 'model-name';
    name.textContent = m.name;
    if (m.active) {
      const badge = document.createElement('span');
      badge.className = 'model-badge';
      badge.textContent = 'ACTIVE';
      name.appendChild(badge);
    }
    const description = document.createElement('span');
    description.className = 'model-desc';
    description.textContent = m.desc;
    const size = document.createElement('span');
    size.className = 'model-size';
    size.textContent = m.size;
    info.append(name, description, size);

    const status = document.createElement('div');
    status.className = 'model-status';
    status.id = `status-${m.id}`;
    if (m.installed) {
      status.appendChild(installedBadge());
    } else if (m.downloadable) {
      status.appendChild(downloadArea(m.id));
    } else {
      status.appendChild(unavailableBadge());
    }
    item.append(radio, info, status);

    radio.disabled = !m.selectable;
    if (m.selectable) {
      radio.addEventListener('change', async () => {
        if (!radio.checked) return;
        if (!m.installed) { await reloadSettings(); return; }

        const res = await window.setActiveModel(m.id);
        if (res.startsWith('error')) { await reloadSettings(); return; }

        // Config written — refresh the active badge then show the restart banner.
        await reloadSettings();
        showRestartBanner();
      });
    }

    container.appendChild(item);

    // Attach download handler
    if (!m.installed && m.downloadable) {
      const btn = item.querySelector('.download-btn');
      if (btn) btn.addEventListener('click', e => { e.stopPropagation(); startDownload(m.id); });
    }
  });
}

// Show a persistent banner prompting the user to restart to apply model changes.
function showRestartBanner() {
  const banner = document.getElementById('restart-banner');
  if (banner) banner.hidden = false;
  scheduleSettingsLayout();
}

function installedBadge() {
  const badge = document.createElement('span');
  badge.className = 'installed-badge';
  badge.textContent = '✓ Installed';
  return badge;
}

function unavailableBadge() {
  const badge = document.createElement('span');
  badge.className = 'installed-badge';
  badge.textContent = 'Not found';
  return badge;
}

function downloadArea(id) {
  const area = document.createElement('div');
  area.className = 'download-area';

  const button = document.createElement('button');
  button.className = 'download-btn';
  button.id = `btn-${id}`;
  button.textContent = '↓ Download';

  const wrap = document.createElement('div');
  wrap.className = 'dl-progress-wrap';
  wrap.id = `prog-wrap-${id}`;
  wrap.hidden = true;

  const progress = document.createElement('progress');
  progress.className = 'dl-progress';
  progress.id = `prog-${id}`;
  progress.value = 0;
  progress.max = 1;

  const label = document.createElement('span');
  label.className = 'dl-progress-label';
  label.id = `pct-${id}`;
  label.textContent = '0%';

  wrap.append(progress, label);
  area.append(button, wrap);
  return area;
}

function startDownload(modelId) {
  const btn      = document.getElementById(`btn-${modelId}`);
  const progWrap = document.getElementById(`prog-wrap-${modelId}`);

  // Show progress, hide button — never show both at once
  if (btn)      btn.hidden      = true;
  if (progWrap) progWrap.hidden = false;

  window.downloadModel(modelId);
}

// Called from Go via webview.Eval — matched by model ID, not name text.
window.onDownloadProgress = (modelId, percent) => {
  const prog = document.getElementById(`prog-${modelId}`);
  const pct  = document.getElementById(`pct-${modelId}`);
  if (prog) prog.value = percent / 100;
  if (pct)  pct.textContent = `${Math.round(percent)}%`;
};

window.onDownloadComplete = (modelId) => {
  const statusDiv = document.getElementById(`status-${modelId}`);
  if (statusDiv) statusDiv.replaceChildren(installedBadge());
  reloadSettings();
};

window.onDownloadError = (modelId, err) => {
  // Restore the download button on failure
  const btn      = document.getElementById(`btn-${modelId}`);
  const progWrap = document.getElementById(`prog-wrap-${modelId}`);
  if (btn)      { btn.hidden = false; }
  if (progWrap) { progWrap.hidden = true; }
  console.error('Download error:', modelId, err);
};

// ---- Language ----
const WHISPER_LANGUAGES = [
  { code: 'auto', name: 'Auto Detect' },
  { code: 'en',   name: 'English' },
  { code: 'de',   name: 'German' },
  { code: 'es',   name: 'Spanish' },
  { code: 'fr',   name: 'French' },
  { code: 'pt',   name: 'Portuguese' },
  { code: 'ru',   name: 'Russian' },
  { code: 'it',   name: 'Italian' },
];

function renderLanguage(currentLang) {
  const select = document.getElementById('language-select');
  if (!select) return;

  select.replaceChildren();
  const active = currentLang || 'en';

  WHISPER_LANGUAGES.forEach(({ code, name }) => {
    const opt = document.createElement('option');
    opt.value = code;
    opt.textContent = name;
    if (code === active) opt.selected = true;
    select.appendChild(opt);
  });

  select.onchange = async () => {
    const res = await window.saveLanguage(select.value);
    if (!res.startsWith('error')) showRestartBanner();
  };
}

// ---- Appearance ----
const THEMES = new Set(['system', 'light', 'dark']);

function applySettingsTheme(theme) {
  const chosen = THEMES.has(theme) ? theme : 'system';
  document.documentElement.dataset.theme = chosen;
}

function showAppearanceStatus(message, isError) {
  const status = document.getElementById('appearance-status');
  if (!status) return;
  status.hidden = !message;
  status.textContent = message || '';
  status.classList.toggle('setting-note-error', !!isError);
  scheduleSettingsLayout();
}

function renderTheme(theme) {
  const select = document.getElementById('appearance-theme');
  if (!select) return;

  let previous = THEMES.has(theme) ? theme : 'system';
  select.value = previous;
  applySettingsTheme(previous);
  select.onchange = async () => {
    const chosen = select.value;
    applySettingsTheme(chosen);
    try {
      const result = await window.saveTheme(chosen);
      if (typeof result === 'string' && result.startsWith('error:')) {
        select.value = previous;
        applySettingsTheme(previous);
        showAppearanceStatus(result.slice('error:'.length).trim(), true);
        return;
      }
      previous = chosen;
      showAppearanceStatus('Saved', false);
    } catch (error) {
      select.value = previous;
      applySettingsTheme(previous);
      showAppearanceStatus(`Could not save theme: ${error}`, true);
    }
  };
}

// ---- Lowercase output ----
function renderLowercaseOutput(enabled) {
  const toggle = document.getElementById('lowercase-toggle');
  if (!toggle) return;
  toggle.checked = !!enabled;
  toggle.onchange = async () => {
    await window.saveLowercaseOutput(toggle.checked);
  };
}

// ---- Raw output (skip LLM cleanup) ----
function renderSkipLLMCleanup(enabled) {
  const toggle = document.getElementById('raw-output-toggle');
  if (!toggle) return;
  toggle.checked = !!enabled;
  toggle.onchange = async () => {
    await window.saveSkipLLMCleanup(toggle.checked);
  };
}

// ---- Personal dictionary ----
// Model downloads and window reopens refresh all settings. Keep an unsaved
// dictionary draft outside renderDictionary so those unrelated refreshes do
// not discard text the user is still editing.
let dictionaryDraft = null;
let dictionaryDirty = false;
let dictionarySaving = false;
let dictionarySaveGeneration = 0;

function showDictionaryStatus(message, isError) {
  const status = document.getElementById("dictionary-status");
  if (!status) return;
  status.hidden = !message;
  status.textContent = message || "";
  status.classList.toggle("setting-note-error", !!isError);
  scheduleSettingsLayout();
}

function sizeDictionaryColumns() {
  const list = document.getElementById("dictionary-list");
  const rows = Array.from(list?.querySelectorAll(".dictionary-entry") || []);
  if (!list || !rows.length || !list.clientWidth) return;

  const sampleInput = rows[0].querySelector("input");
  const sampleButton = rows[0].querySelector("button");
  if (!sampleInput || !sampleButton) return;

  const inputStyle = getComputedStyle(sampleInput);
  const rowStyle = getComputedStyle(rows[0]);
  const canvas = document.createElement("canvas");
  const context = canvas.getContext("2d");
  if (!context) return;
  context.font = inputStyle.font ||
    `${inputStyle.fontWeight} ${inputStyle.fontSize} ${inputStyle.fontFamily}`;

  let widestTerm = 0;
  rows.forEach(row => {
    const input = row.querySelector("input");
    const text = input?.value || input?.placeholder || "";
    widestTerm = Math.max(widestTerm, context.measureText(text).width);
  });

  const inputChrome =
    (Number.parseFloat(inputStyle.paddingLeft) || 0) +
    (Number.parseFloat(inputStyle.paddingRight) || 0) +
    (Number.parseFloat(inputStyle.borderLeftWidth) || 0) +
    (Number.parseFloat(inputStyle.borderRightWidth) || 0);
  const rowChrome =
    (Number.parseFloat(rowStyle.paddingLeft) || 0) +
    (Number.parseFloat(rowStyle.paddingRight) || 0) +
    (Number.parseFloat(rowStyle.columnGap) || 0);
  const inputWidth = Math.max(96, Math.ceil(widestTerm + inputChrome + 4));
  const preferredWidth =
    inputWidth + Math.ceil(sampleButton.getBoundingClientRect().width) + rowChrome;
  list.style.setProperty("--dictionary-entry-width", `${preferredWidth}px`);
}

function renderDictionary(terms) {
  const list = document.getElementById("dictionary-list");
  const add = document.getElementById("dictionary-add-btn");
  const save = document.getElementById("dictionary-save-btn");
  if (!list || !add || !save) return;

  const draft = dictionaryDirty
    ? Array.from(dictionaryDraft || [])
    : Array.from(terms || []);
  dictionaryDraft = Array.from(draft);
  const markDirty = () => {
    dictionaryDraft = Array.from(draft);
    dictionaryDirty = true;
    save.disabled = false;
    showDictionaryStatus("Unsaved changes", false);
  };

  const renderRows = () => {
    list.replaceChildren();
    if (draft.length === 0) {
      const empty = document.createElement("div");
      empty.className = "dictionary-empty";
      empty.textContent = "No personal terms yet";
      list.appendChild(empty);
      scheduleSettingsLayout();
      return;
    }

    draft.forEach((term, index) => {
      const row = document.createElement("div");
      row.className = "dictionary-entry";

      const input = document.createElement("input");
      input.type = "text";
      input.className = "setting-input dictionary-input";
      input.value = term;
      input.autocomplete = "off";
      input.spellcheck = false;
      input.placeholder = "Sussurro";
      input.setAttribute("aria-label", `Dictionary term ${index + 1}`);
      input.oninput = () => {
        draft[index] = input.value;
        markDirty();
      };
      input.onkeydown = (event) => {
        if (event.key !== "Enter" || event.isComposing) return;
        event.preventDefault();
        save.click();
      };

      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "hotkey-edit-btn dictionary-remove-btn";
      remove.textContent = "Remove";
      remove.setAttribute(
        "aria-label",
        `Remove dictionary term ${index + 1}`,
      );
      remove.onclick = () => {
        draft.splice(index, 1);
        renderRows();
        markDirty();
        const inputs = list.querySelectorAll("input");
        if (inputs.length === 0) {
          add.focus();
        } else {
          inputs[Math.min(index, inputs.length - 1)].focus();
        }
      };

      row.append(input, remove);
      list.appendChild(row);
    });
    scheduleSettingsLayout();
  };

  add.onclick = () => {
    draft.push("");
    renderRows();
    markDirty();
    const inputs = list.querySelectorAll("input");
    inputs[inputs.length - 1]?.focus();
  };

  const setControlsDisabled = (disabled) => {
    add.disabled = disabled;
    save.disabled = disabled;
    list.querySelectorAll("input, button").forEach((control) => {
      control.disabled = disabled;
    });
  };

  save.onclick = async () => {
    const normalized = draft.map((term) => term.trim());
    const generation = ++dictionarySaveGeneration;
    dictionarySaving = true;
    setControlsDisabled(true);
    showDictionaryStatus("Saving…", false);
    try {
      const result = await window.saveDictionary(JSON.stringify(normalized));
      if (generation !== dictionarySaveGeneration) return;
      dictionarySaving = false;
      if (typeof result === "string" && result.startsWith("error:")) {
        renderDictionary(dictionaryDraft);
        showDictionaryStatus(result.slice("error:".length).trim(), true);
        return;
      }
      dictionaryDraft = Array.from(normalized);
      dictionaryDirty = false;
      renderDictionary(normalized);
      showDictionaryStatus(
        "Saved. Changes apply to the next dictation",
        false,
      );
    } catch (error) {
      if (generation !== dictionarySaveGeneration) return;
      dictionarySaving = false;
      renderDictionary(dictionaryDraft);
      showDictionaryStatus(`Could not save dictionary: ${error}`, true);
    }
  };

  renderRows();
  setControlsDisabled(dictionarySaving);
  save.disabled = dictionarySaving || !dictionaryDirty;
  showDictionaryStatus(
    dictionarySaving ? "Saving…" : dictionaryDirty ? "Unsaved changes" : "",
    false,
  );
}

// ---- Review workflow ----

// Populates a <select> from the capability list Go supplied. Unavailable
// options stay visible but disabled, with the reason in the label, so the user
// can tell "not offered here" from "needs something installed".
function fillChoices(select, choices, current) {
  if (!select) return;
  select.replaceChildren();
  (choices || []).forEach(choice => {
    const option = document.createElement('option');
    option.value = choice.value;
    option.textContent = choice.reason
      ? `${choice.label} — ${choice.reason}`
      : choice.label;
    // Never disable the value already in the config: the user must be able to
    // see what is selected, even if this host cannot honour it.
    option.disabled = !choice.available && choice.value !== current;
    select.appendChild(option);
  });
  select.value = current;
}

// Shows the outcome of a save. Validation errors come from the same validator
// the config file uses, so the message is worth surfacing verbatim.
function showWorkflowStatus(message, isError) {
  const status = document.getElementById('workflow-status');
  if (!status) return;
  status.hidden = !message;
  status.textContent = message || '';
  status.classList.toggle('setting-note-error', !!isError);
  scheduleSettingsLayout();
}

// Saves one workflow setting, reverting the control if Go rejects the value.
async function saveWorkflow(key, value, revert) {
  const result = await window.saveWorkflowSetting(key, String(value));
  if (typeof result === 'string' && result.startsWith('error:')) {
    showWorkflowStatus(result.slice('error:'.length).trim(), true);
    if (revert) revert();
    return false;
  }
  showWorkflowStatus('Saved', false);
  return true;
}

function renderWorkflow(workflow) {
  if (!workflow) return;

  const modeSelect     = document.getElementById('workflow-mode');
  const streaming      = document.getElementById('workflow-streaming-toggle');
  const interval       = document.getElementById('workflow-streaming-interval');
  const deliverySelect = document.getElementById('workflow-delivery-backend');
  const inputSelect    = document.getElementById('workflow-input-backend');
  const device         = document.getElementById('workflow-input-device');
  const chord          = document.getElementById('workflow-input-chord');
  const cancelChord    = document.getElementById('workflow-input-cancel-chord');

  fillChoices(modeSelect, workflow.modes, workflow.mode);
  fillChoices(deliverySelect, workflow.deliveryBackends, workflow.deliveryBackend);
  fillChoices(inputSelect, workflow.inputBackends, workflow.inputBackend);

  // The evdev-only rows are meaningless for any other input source.
  const showEvdevRows = backend => {
    const evdev = backend === 'evdev';
    ['workflow-evdev-row', 'workflow-chord-row', 'workflow-cancel-chord-row'].forEach(id => {
      const row = document.getElementById(id);
      if (row) row.hidden = !evdev;
    });
  };
  showEvdevRows(workflow.inputBackend);

  const renderVoiceEditing = mode => {
    const desc = document.getElementById('workflow-voice-editing-desc');
    if (!desc) return;
    desc.textContent = mode === 'review'
      ? 'Hold the hotkey over reviewed text to dictate a correction'
      : 'Available in review mode';
  };
  renderVoiceEditing(workflow.mode);

  if (modeSelect) {
    let previous = workflow.mode;
    modeSelect.onchange = async () => {
      const chosen = modeSelect.value;
      const ok = await saveWorkflow('workflow.mode', chosen, () => { modeSelect.value = previous; });
      if (ok) {
        previous = chosen;
        renderVoiceEditing(chosen);
      }
    };
  }

  if (streaming) {
    streaming.checked = !!workflow.streamingEnabled;
    streaming.onchange = async () => {
      await saveWorkflow('workflow.streaming.enabled', streaming.checked,
        () => { streaming.checked = !streaming.checked; });
    };
  }


  bindTextSetting(interval, workflow.streamingInterval, 'workflow.streaming.interval');
  bindTextSetting(device, workflow.inputDevice, 'workflow.input.device');
  bindTextSetting(chord, workflow.inputChord, 'workflow.input.chord');
  bindTextSetting(cancelChord, workflow.inputCancelChord, 'workflow.input.cancel_chord');

  if (deliverySelect) {
    let previous = workflow.deliveryBackend;
    deliverySelect.onchange = async () => {
      const chosen = deliverySelect.value;
      const ok = await saveWorkflow('workflow.delivery.backend', chosen,
        () => { deliverySelect.value = previous; });
      if (ok) previous = chosen;
    };
  }

  if (inputSelect) {
    let previous = workflow.inputBackend;
    inputSelect.onchange = async () => {
      const chosen = inputSelect.value;
      const ok = await saveWorkflow('workflow.input.backend', chosen,
        () => { inputSelect.value = previous; });
      if (!ok) return;
      previous = chosen;
      showEvdevRows(chosen);
      showWorkflowStatus('Saved. Restart Sussurro for the new input source to take effect', false);
    };
  }
}

// Binds a text field that saves on blur or Enter, reverting a rejected value.
function bindTextSetting(field, initial, key) {
  if (!field) return;
  field.value = initial || '';

  let previous = field.value;
  const commit = async () => {
    if (field.value === previous) return;
    const chosen = field.value;
    const ok = await saveWorkflow(key, chosen, () => { field.value = previous; });
    if (ok) previous = chosen;
  };

  field.onblur = commit;
  field.onkeydown = event => {
    if (event.key === 'Enter') {
      event.preventDefault();
      field.blur();
    }
  };
}

// ---- Hotkey ----
// Push-to-talk and toggle are independent bindings, either of which may be
// unset. There is no mode: each key does what it is bound to do.
function renderHotkey(pushToTalk, toggle, isWayland) {
  const pttRow     = document.getElementById('hotkey-x11');
  const toggleRow  = document.getElementById('hotkey-toggle-row');
  const waylandRow = document.getElementById('hotkey-wayland');

  if (isWayland) {
    if (pttRow)     pttRow.hidden    = true;
    if (toggleRow)  toggleRow.hidden = true;
    if (waylandRow) waylandRow.hidden = false;
    return;
  }

  if (waylandRow) waylandRow.hidden = true;

  bindHotkeyRow(pttRow, 'hotkey-display', 'hotkey-edit-btn',
                pushToTalk, window.savePushToTalkHotkey);
  bindHotkeyRow(toggleRow, 'hotkey-toggle-display', 'hotkey-toggle-edit-btn',
                toggle, window.saveToggleHotkey);
}

function bindHotkeyRow(row, displayId, buttonId, trigger, save) {
  if (!row) return;
  row.hidden = false;

  updateHotkeyDisplay(displayId, trigger);

  const btn = document.getElementById(buttonId);
  if (btn) {
    btn.onclick = () => showRecordModal((combo) => save(combo).then((res) => {
        if (typeof res !== 'string' || !res.startsWith('error')) {
          updateHotkeyDisplay(displayId, combo);
        }
        return res;
      }));
  }
}

function updateHotkeyDisplay(displayId, trigger) {
  const display = document.getElementById(displayId);
  if (!display) return;
  display.replaceChildren();
  if (!trigger) {
    // An unset binding says so rather than rendering an empty row.
    const unset = document.createElement('span');
    unset.className = 'hotkey-unset';
    unset.textContent = 'Not set';
    display.appendChild(unset);
    return;
  }
  trigger.split('+').forEach((key, index) => {
    if (index > 0) {
      const separator = document.createElement('span');
      separator.className = 'hotkey-separator';
      separator.textContent = '+';
      display.appendChild(separator);
    }
    const keycap = document.createElement('kbd');
    keycap.textContent = key;
    display.appendChild(keycap);
  });
}

// ---- Record hotkey modal ----
const MAX_HOTKEY_KEYS = 3;
const MODIFIER_KEY_NAMES = new Set(['ctrl', 'shift', 'alt', 'super']);

function keyNameFromEvent(e) {
  switch (e.key) {
    case 'Control': return 'ctrl';
    case 'Shift':   return 'shift';
    case 'Alt':     return 'alt';
    case 'Meta':    return 'super';
    default: {
      const k = e.key.toLowerCase();
      return k === ' ' ? 'space' : k;
    }
  }
}

function buildTriggerFromSet(keys) {
  const mods = [...keys].filter(k =>  MODIFIER_KEY_NAMES.has(k));
  const main = [...keys].filter(k => !MODIFIER_KEY_NAMES.has(k));
  return [...mods, ...main].join('+');
}

function showRecordModal(save) {
  const modal   = document.getElementById('hotkey-modal');
  const preview = document.getElementById('hotkey-modal-preview');
  if (!modal) return;
  modal.classList.add('visible');

  const keysHeld = new Set();
  let lastCombo  = '';
  let finalized  = false;

  function updatePreview() {
    if (!preview) return;
    if (keysHeld.size === 0) {
      preview.textContent = lastCombo || 'Press keys…';
    } else {
      preview.textContent = buildTriggerFromSet(keysHeld);
    }
  }

  function cleanup() {
    document.removeEventListener('keydown', downHandler);
    document.removeEventListener('keyup',   upHandler);
  }

  function downHandler(e) {
    e.preventDefault();
    if (finalized) return;
    const name = keyNameFromEvent(e);
    // Cap at MAX_HOTKEY_KEYS — ignore extra keys if already full
    if (keysHeld.size < MAX_HOTKEY_KEYS) keysHeld.add(name);
    updatePreview();
  }

  async function upHandler(e) {
    e.preventDefault();
    if (finalized) return;
    // Snapshot the full combo on the first key release
    if (lastCombo === '' && keysHeld.size > 0) {
      lastCombo = buildTriggerFromSet(keysHeld);
    }
    const name = keyNameFromEvent(e);
    keysHeld.delete(name);
    updatePreview();
    // Finalize once all keys are released
    if (keysHeld.size === 0 && lastCombo !== '') {
      // Must contain at least one non-modifier key
      const parts = lastCombo.split('+');
      const hasMainKey = parts.some(p => !MODIFIER_KEY_NAMES.has(p));
      if (!hasMainKey) {
        // Only modifiers were pressed — reset and keep waiting
        lastCombo = '';
        updatePreview();
        return;
      }
      finalized = true;
      cleanup();
      await save(lastCombo);
      modal.classList.remove('visible');
    }
  }

  document.addEventListener('keydown', downHandler);
  document.addEventListener('keyup',   upHandler);

  const cancelBtn = document.getElementById('hotkey-modal-cancel');
  if (cancelBtn) {
    cancelBtn.onclick = () => {
      finalized = true;
      cleanup();
      modal.classList.remove('visible');
    };
  }
}
