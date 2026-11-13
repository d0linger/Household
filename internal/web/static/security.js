(() => {
  'use strict';
  const t = (text, ...args) => window.HouseholdI18n?.t(text, ...args) ?? text;
  const dialog = document.querySelector('#security-reauth');
  let afterConfirmation = null;
  let previousFocus = null;
  const csrf = () => document.querySelector('input[name="csrf"]')?.value || document.querySelector('meta[name="csrf-token"]')?.content || '';
  const status = (scope, text, error = false) => {
    let output = scope.querySelector('[data-security-status], [data-passkey-status]');
    if (!output) { output = document.createElement('p'); output.dataset.securityStatus = ''; output.setAttribute('role', 'status'); scope.append(output); }
    output.textContent = text;
    output.hidden = false;
    output.classList.toggle('security-error', error);
  };
  const busy = (scope, value) => {
    scope.setAttribute('aria-busy', String(value));
    scope.querySelectorAll('button').forEach(button => { button.disabled = value; });
  };
  const request = async (url, options = {}) => {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 20000);
    try {
      const response = await fetch(url, { credentials: 'same-origin', signal: controller.signal, ...options,
        headers: { Accept: 'application/json', 'X-CSRF-Token': csrf(), ...options.headers } });
      let data = {};
      if (response.headers.get('content-type')?.includes('application/json')) data = await response.json();
      if (!response.ok) { const error = new Error(data.error || t('Vorgang fehlgeschlagen. Bitte erneut versuchen.')); error.code = data.code; throw error; }
      return data;
    } catch (error) {
      if (error.name === 'AbortError') throw new Error(t('Die Anfrage hat zu lange gedauert. Prüfe den aktuellen Stand, bevor du es erneut versuchst.'));
      throw error;
    } finally { clearTimeout(timeout); }
  };
  const openConfirmation = (callback, returnFocus = document.activeElement) => {
    if (!dialog) { location.assign('/settings/security/reauth'); return; }
    afterConfirmation = callback || null;
    previousFocus = returnFocus;
    dialog.querySelector('form').reset();
    dialog.querySelectorAll('[role="status"]').forEach(node => { node.textContent = ''; });
    dialog.showModal();
    dialog.querySelector('input[name="password"]').focus();
  };
  const confirmed = async () => {
    const next = afterConfirmation;
    afterConfirmation = null;
    if (dialog?.open) dialog.close();
    document.querySelectorAll('.security-freshness p').forEach(node => { node.textContent = t('Deine Anmeldung ist für sensible Änderungen bestätigt.'); });
    if (next) await next();
    else {
      const page = document.querySelector('[data-reauth-page]');
      if (page) location.assign(page.querySelector('[name="return_to"]')?.value || '/settings/security');
    }
  };
  dialog?.addEventListener('close', () => { afterConfirmation = null; dialog.querySelector('form').reset(); previousFocus?.focus(); });
  document.querySelector('[data-close-reauth]')?.addEventListener('click', () => dialog.close());
  document.querySelectorAll('[data-open-reauth]').forEach(link => link.addEventListener('click', event => { event.preventDefault(); openConfirmation(); }));

  const submitForm = async form => {
    if (form.getAttribute('aria-busy') === 'true') return;
    const returnFocus = form.contains(document.activeElement) ? document.activeElement
      : form.querySelector('button:not([type="button"]):not([type="reset"])');
    if (form.hasAttribute('data-security-document')) {
      busy(form, true);
      try {
        const fresh = await request('/api/v1/security/freshness');
        if (!fresh.recent) { openConfirmation(() => submitForm(form), returnFocus); return; }
        HTMLFormElement.prototype.submit.call(form);
      } catch (error) { status(form, error.message, true); }
      finally { busy(form, false); }
      return;
    }
    const body = new URLSearchParams(new FormData(form));
    busy(form, true); status(form, t('Wird gespeichert …'));
    try {
      await request(form.action, { method: 'POST', body });
      location.reload();
    } catch (error) {
      status(form, error.message, true);
      if (error.code === 'reauth_required') openConfirmation(() => submitForm(form), returnFocus);
    } finally { busy(form, false); }
  };
  document.querySelectorAll('[data-security-form]').forEach(form => form.addEventListener('submit', event => { event.preventDefault(); void submitForm(form); }));
  document.querySelectorAll('[data-reauth-form], [data-reauth-page]').forEach(form => form.addEventListener('submit', async event => {
    event.preventDefault();
    if (form.getAttribute('aria-busy') === 'true') return;
    const body = new URLSearchParams(new FormData(form));
    busy(form, true); status(form, t('Wird bestätigt …'));
    try { await request(form.action, { method: 'POST', body }); form.reset(); await confirmed(); }
    catch (error) { status(form, error.message, true); }
    finally { busy(form, false); }
  }));

  const decode = value => Uint8Array.from(atob(value.replaceAll('-', '+').replaceAll('_', '/')), letter => letter.charCodeAt(0));
  const encode = value => { const bytes = new Uint8Array(value); let raw = ''; for (const byte of bytes) raw += String.fromCharCode(byte); return btoa(raw).replaceAll('+', '-').replaceAll('/', '_').replaceAll('=', ''); };
  const createOptions = value => {
    if (typeof PublicKeyCredential.parseCreationOptionsFromJSON === 'function') return PublicKeyCredential.parseCreationOptionsFromJSON(value);
    return { ...value, challenge: decode(value.challenge), user: { ...value.user, id: decode(value.user.id) }, excludeCredentials: (value.excludeCredentials || []).map(key => ({ ...key, id: decode(key.id) })) };
  };
  const getOptions = value => {
    if (typeof PublicKeyCredential.parseRequestOptionsFromJSON === 'function') return PublicKeyCredential.parseRequestOptionsFromJSON(value);
    return { ...value, challenge: decode(value.challenge), allowCredentials: (value.allowCredentials || []).map(key => ({ ...key, id: decode(key.id) })) };
  };
  const serialize = key => {
    if (typeof key.toJSON === 'function') return key.toJSON();
    const response = { clientDataJSON: encode(key.response.clientDataJSON) };
    if (key.response.attestationObject) { response.attestationObject = encode(key.response.attestationObject); response.transports = key.response.getTransports?.() || []; }
    else { response.authenticatorData = encode(key.response.authenticatorData); response.signature = encode(key.response.signature); response.userHandle = key.response.userHandle ? encode(key.response.userHandle) : null; }
    return { id: key.id, rawId: encode(key.rawId), type: key.type, response, clientExtensionResults: key.getClientExtensionResults(), authenticatorAttachment: key.authenticatorAttachment };
  };
  const ceremony = async (purpose, scope) => {
    if (!window.isSecureContext || !window.PublicKeyCredential || !navigator.credentials) { status(scope, t('Dieser Browser unterstützt hier keine Passkeys. Verwende dein Passwort.'), true); return; }
    if (scope.getAttribute('aria-busy') === 'true') return;
    const base = purpose === 'login' ? '/api/v1/auth/passkeys/login' : `/api/v1/security/passkeys/${purpose}`;
    busy(scope, true); status(scope, t('Folge der Bestätigung auf deinem Gerät.'));
    try {
      const start = await request(`${base}/begin`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: purpose === 'register' ? scope.querySelector('[name="name"]').value : '' }) });
      const key = purpose === 'register' ? await navigator.credentials.create({ publicKey: createOptions(start.options) }) : await navigator.credentials.get({ publicKey: getOptions(start.options) });
      if (!key) throw new Error(t('Die Passkey-Bestätigung wurde abgebrochen.'));
      await request(`${base}/finish`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ challenge_id: start.challenge_id, credential: serialize(key) }) });
      if (purpose === 'reauth') await confirmed();
      else location.assign(purpose === 'login' ? '/' : '/settings/security');
    } catch (error) {
      const message = ['NotAllowedError', 'AbortError'].includes(error.name) ? t('Die Passkey-Bestätigung wurde abgebrochen oder ist abgelaufen. Du kannst es erneut versuchen.') : error.message;
      status(scope, message, true);
      if (error.code === 'reauth_required') openConfirmation(() => ceremony(purpose, scope));
    } finally { busy(scope, false); }
  };
  document.querySelector('[data-passkey-register]')?.addEventListener('submit', event => { event.preventDefault(); void ceremony('register', event.currentTarget); });
  document.querySelectorAll('[data-passkey-reauth]').forEach(button => button.addEventListener('click', () => { void ceremony('reauth', button.parentElement); }));
  document.querySelector('[data-passkey-login]')?.addEventListener('click', event => { void ceremony('login', event.currentTarget.parentElement); });
})();
