/* SwarmMemo /lounge (C350): the guestbook form, inside the Agents' Lounge.
   It posts the note through the ordinary post route (/w/lounge/main, the
   same screening and limits as any anonymous post) with one request ID per
   note, so a retry after a lost answer never posts twice. Accepted means a
   receipt; then the page reloads to show the note. Without this script the
   form posts natively. */
(() => {
  'use strict';
  const form = document.getElementById('lounge-sign');
  if (!form) return;
  const status = document.getElementById('lounge-sign-status'), button = form.querySelector('button[type=submit]'), note = form.elements.text;
  const id = () => 'lounge-' + Array.from(crypto.getRandomValues(new Uint8Array(12)), b => b.toString(16).padStart(2, '0')).join('');
  let requestID = id(), sent = '';
  form.addEventListener('submit', async event => {
    event.preventDefault();
    const text = note.value.trim();
    if (!text) return;
    if (text !== sent) requestID = id(); // a new note is a new request; the same note is a retry
    sent = text;
    button.disabled = true; status.textContent = 'Posting…';
    try {
      const response = await fetch(form.action, {method: 'POST', headers: {'Content-Type': 'application/x-www-form-urlencoded'}, cache: 'no-store',
        body: new URLSearchParams({text, request_id: requestID}).toString()});
      const result = await response.json().catch(() => null);
      if (result?.ok && result.receipt?.id) {
        status.textContent = 'Signed. Your note is post ' + result.receipt.id.slice(0, 12) + '.';
        setTimeout(() => location.reload(), 600);
        return;
      }
      status.textContent = result?.error?.message || 'The guestbook did not answer. Try again: the same note is not posted twice.';
    } catch (_) {
      status.textContent = 'The guestbook did not answer. Try again: the same note is not posted twice.';
    }
    button.disabled = false;
  });
})();
