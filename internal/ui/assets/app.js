'use strict';

// ---- Bootstrap ----
document.addEventListener('DOMContentLoaded', async () => {
  initFoldableSections();
  await reloadSettings();
});

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
  // Status bar labels
  document.getElementById('platform-label').textContent = data.platform;
  document.getElementById('version-label').textContent  = `v${data.version}`;

  // Models
  const whisperItems = data.models.filter(m => m.type === 'whisper');
  const llmItems     = data.models.filter(m => m.type === 'llm');
  renderModelList('whisper-list', whisperItems, 'whisper');
  renderModelList('llm-list',     llmItems,     'llm');

  // Hotkey
  renderHotkey(data.hotkey, data.hotkeyMode, data.isWayland);

  // Language
  renderLanguage(data.language);

  // Lowercase output
  renderLowercaseOutput(data.lowercaseOutput);
  renderSkipLLMCleanup(data.skipLLMCleanup);
  renderWorkflow(data.workflow);
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
    };
  });
}

// ---- Model list ----
function renderModelList(containerId, models, groupName) {
  const container = document.getElementById(containerId);
  container.innerHTML = '';

  models.forEach(m => {
    const item = document.createElement('div');
    item.className = 'model-item' + (m.active ? ' active' : '');
    item.dataset.id = m.id;

    item.innerHTML = `
      <input type="radio" name="${groupName}" value="${m.id}" ${m.active ? 'checked' : ''}>
      <div class="model-info">
        <span class="model-name">${m.name}${m.active ? '<span class="model-badge">ACTIVE</span>' : ''}</span>
        <span class="model-desc">${m.desc}</span>
        <span class="model-size">${m.size}</span>
      </div>
      <div class="model-status" id="status-${m.id}">
        ${m.installed ? installedBadge() : downloadArea(m.id)}
      </div>
    `;

    const radio = item.querySelector('input[type="radio"]');

    // LLM has only one model — disable the radio, nothing to switch to
    if (m.type === 'llm') {
      radio.disabled = true;
    } else {
      radio.addEventListener('change', async () => {
        if (!radio.checked) return;
        if (!m.installed) { radio.checked = false; return; }

        const res = await window.setActiveModel(m.id);
        if (res.startsWith('error')) { radio.checked = false; return; }

        // Config written — refresh the active badge then show the restart banner.
        await reloadSettings();
        showRestartBanner();
      });
    }

    container.appendChild(item);

    // Attach download handler
    if (!m.installed) {
      const btn = item.querySelector('.download-btn');
      if (btn) btn.addEventListener('click', e => { e.stopPropagation(); startDownload(m.id, m.name); });
    }
  });
}

// Show a persistent banner prompting the user to restart to apply model changes.
function showRestartBanner() {
  const banner = document.getElementById('restart-banner');
  if (banner) banner.hidden = false;
}

function installedBadge() {
  return `<span class="installed-badge">✓ Installed</span>`;
}

function downloadArea(id) {
  return `
    <div class="download-area">
      <button class="download-btn" id="btn-${id}">↓ Download</button>
      <div class="dl-progress-wrap" id="prog-wrap-${id}" hidden>
        <progress class="dl-progress" id="prog-${id}" value="0" max="1"></progress>
        <span class="dl-progress-label" id="pct-${id}">0%</span>
      </div>
    </div>
  `;
}

function startDownload(modelId, modelName) {
  const btn      = document.getElementById(`btn-${modelId}`);
  const progWrap = document.getElementById(`prog-wrap-${modelId}`);

  // Show progress, hide button — never show both at once
  if (btn)      btn.hidden      = true;
  if (progWrap) progWrap.hidden = false;

  window.downloadModel(modelId);
}

// Called from Go via webview.Eval — matched by model ID, not name text.
window.onDownloadProgress = function(modelId, percent) {
  const prog = document.getElementById(`prog-${modelId}`);
  const pct  = document.getElementById(`pct-${modelId}`);
  if (prog) prog.value = percent / 100;
  if (pct)  pct.textContent = `${Math.round(percent)}%`;
};

window.onDownloadComplete = function(modelId) {
  const statusDiv = document.getElementById(`status-${modelId}`);
  if (statusDiv) statusDiv.innerHTML = installedBadge();
  reloadSettings();
};

window.onDownloadError = function(modelId, err) {
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

  select.innerHTML = '';
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

// ---- Review workflow ----

// Populates a <select> from the capability list Go supplied. Unavailable
// options stay visible but disabled, with the reason in the label, so the user
// can tell "not offered here" from "needs something installed".
function fillChoices(select, choices, current) {
  if (!select) return;
  select.innerHTML = '';
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
function renderHotkey(trigger, mode, isWayland) {
  const x11Row     = document.getElementById('hotkey-x11');
  const modeRow    = document.getElementById('hotkey-mode-row');
  const waylandRow = document.getElementById('hotkey-wayland');

  if (isWayland) {
    if (x11Row)     x11Row.hidden     = true;
    if (modeRow)    modeRow.hidden    = true;
    if (waylandRow) waylandRow.hidden = false;
    return;
  }

  if (waylandRow) waylandRow.hidden = true;
  if (!x11Row) return;
  x11Row.hidden = false;

  updateHotkeyDisplay(trigger);

  const editBtn = document.getElementById('hotkey-edit-btn');
  if (editBtn) editBtn.onclick = () => showRecordModal(trigger);

  // Mode selector
  if (modeRow) {
    modeRow.hidden = false;
    const sel = document.getElementById('hotkey-mode-select');
    if (sel) {
      sel.value = mode || 'push-to-talk';
      sel.onchange = async () => {
        await window.saveHotkeyMode(sel.value);
      };
    }
  }
}

function updateHotkeyDisplay(trigger) {
  const display = document.getElementById('hotkey-display');
  if (!display) return;
  display.innerHTML = trigger.split('+')
    .map(k => `<kbd>${k}</kbd>`)
    .join('<span style="color:var(--muted);font-size:11px;padding:0 2px">+</span>');
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

function showRecordModal(currentTrigger) {
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
      const res = await window.saveHotkey(lastCombo);
      modal.classList.remove('visible');
      if (!res.startsWith('error')) updateHotkeyDisplay(lastCombo);
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
