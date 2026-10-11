// Requires an owned, verified disposable loopback preview; never production.
// The header on a phone with new replies (C181): Me always opens Me, where
// Settings is one tap away; the inbox is its own control beside it, saying
// "2 replies", and lists each reply by its permalink. Each opens on the reply
// itself, centred and marked New, even past the conversation's first page and
// deeper than the page indents.
const assert = require('node:assert/strict');
const {identity, board, withIdentity, launch, noHorizontalScroll} = require('./testdata/messages_mock.cjs');
const origin = process.env.SWARMMEMO_TEST_URL;
assert.match(origin || '', /^http:\/\/127\.0\.0\.1:\d+$/, 'requires an explicit disposable loopback preview');
const shots = process.env.SWARMMEMO_SCREENSHOT_DIR;

(async () => {
  const browser = await launch();
  try {
    const me = identity('pliplop'), b = board(); b.addAgent(me);
    const context = await browser.newContext({viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true, deviceScaleFactor: 2});
    await withIdentity(context, me);
    // Signed by the key itself, so no anonymous thread limit applies.
    const write = async (text, reply_to) => {
      const answer = await (await context.request.post(origin + '/v1/command', {data: me.sign({operation: 'post', room: 'lobby', page: 'main', visibility: 'public', text, ...(reply_to ? {reply_to} : {})}).command})).json();
      assert.equal(answer.ok, true, JSON.stringify(answer)); return answer.receipt.id;
    };
    // A long conversation: a root, 44 replies (the last past the first page of 40),
    // then a chain of 7 under that last one, deeper than the page indents (5).
    const stamp = Date.now(), root = await write('A long conversation for the phone ' + stamp);
    let late = '';
    for (let i = 1; i <= 44; i++) late = await write('Reply ' + i + ' of a long conversation ' + stamp, root);
    let deep = late;
    for (let d = 1; d <= 7; d++) deep = await write('Deep reply ' + d + ' ' + stamp, deep);
    // Two new replies for this key, as the notifier keeps them (read a moment ago, so no read is due).
    await context.addInitScript(([fp, ids]) => { try { const slot = 'swarmmemo.notify.v1:' + fp; if (!localStorage.getItem(slot)) localStorage.setItem(slot, JSON.stringify({fp, cursor: 'u0', polled_at: Date.now(), rooms: 0, requests: 0, replies: ids, addressed: []})); } catch (_) { /* opaque origins */ } }, [me.fingerprint, [late, deep]]);
    const page = await context.newPage(); const errors = []; page.on('pageerror', e => errors.push(e.message));
    await b.attach(page);

    await page.goto(origin + '/?sort=new');
    const inbox = page.locator('#me-count-button'), meLink = page.locator('.workspace-link'), panel = page.locator('#inbox-menu .inbox-panel');
    await inbox.waitFor({state: 'visible'});
    assert.equal(await page.locator('#me-count').textContent(), '2 replies', 'the inbox says what is new');
    assert.equal(await meLink.getAttribute('href'), '/me', 'Me always leads to Me, news or not');
    assert.ok(await noHorizontalScroll(page), 'no horizontal scroll at 390px');
    for (const target of [inbox, meLink]) {
      const box = await target.boundingBox();
      assert.ok(box && box.y + box.height <= 120 && box.x >= 0 && box.x + box.width <= 390, 'both sit in the header, on screen');
      assert.ok(box.height >= 32, 'a tap target');
    }
    const [a, m] = [await inbox.boundingBox(), await meLink.boundingBox()];
    assert.ok(a.x + a.width <= m.x, 'the inbox and Me do not overlap');
    if (shots) await page.locator('header.topbar').screenshot({path: shots + '/notifier-mobile-header-390.png'});

    // Tapping Me opens Me; Settings is one tap from there. The replies are still new.
    await meLink.tap(); await page.waitForURL(origin + '/me');
    await page.getByRole('heading', {name: 'Me', exact: true}).waitFor({state: 'attached'});
    const settings = page.locator('#me-tabs a[href="#more"]');
    assert.ok(await settings.isVisible(), 'Settings is in view on Me');
    await settings.tap(); await page.locator('#more').waitFor({state: 'visible'});
    assert.equal(new URL(page.url()).hash, '#more');
    assert.equal(await page.locator('#me-count').textContent(), '2 replies', 'visiting Me does not clear the replies');

    // Tapping the inbox lists each reply, oldest first, by its permalink, with what it says.
    await inbox.tap(); await panel.waitFor({state: 'visible'});
    const items = page.locator('#inbox-items a');
    assert.deepEqual(await items.evaluateAll(list => list.map(a => a.getAttribute('href'))), ['/e/' + late, '/e/' + deep]);
    assert.equal(await items.first().locator('.inbox-item-kind').textContent(), 'Reply to you');
    await page.waitForFunction(() => document.querySelectorAll('#inbox-items .inbox-item-preview').length === 2);
    assert.match(await items.first().locator('.inbox-item-preview').textContent(), /Reply 44 of a long conversation/);
    const box = await panel.boundingBox();
    assert.ok(box.x >= 0 && box.x + box.width <= 390, 'the list fits the phone');
    assert.ok(await noHorizontalScroll(page), 'no horizontal scroll with the list open');
    if (shots) await page.screenshot({path: shots + '/notifier-mobile-inbox-390.png'});
    // A tap elsewhere closes it.
    await page.locator('main').tap({position: {x: 20, y: 400}});
    await panel.waitFor({state: 'hidden'});

    const centred = id => page.waitForFunction(id => { const r = document.getElementById('e-' + id).getBoundingClientRect(), mid = r.top + Math.min(r.height, innerHeight) / 2; return r.top >= 0 && Math.abs(mid - innerHeight / 2) < innerHeight / 4; }, id);
    const marked = async id => {
      const el = page.locator('#e-' + id);
      assert.ok(await el.evaluate(n => n.classList.contains('memo-focus') && n.classList.contains('memo-new')), 'the reply is highlighted');
      assert.equal(await el.locator('.memo-new-label').textContent(), 'New reply');
      assert.notEqual(await el.evaluate(n => getComputedStyle(n).backgroundColor), 'rgba(0, 0, 0, 0)');
    };
    // The reply past the first page: the conversation reads on to it.
    await inbox.tap(); await items.first().tap(); await page.waitForURL(origin + '/e/' + late);
    await page.locator('#e-' + late).waitFor();
    assert.ok(await page.locator('#thread article.memo').count() > 40, 'the conversation is read on past its first page to the reply');
    await centred(late); await marked(late);
    if (shots) await page.screenshot({path: shots + '/notifier-mobile-reply-390.png'});
    await page.waitForFunction(() => document.getElementById('me-count').textContent === '1 reply');

    // The reply deeper than the page indents: its branch, with the way back up.
    await inbox.tap(); await items.first().tap(); await page.waitForURL(origin + '/e/' + deep);
    await page.locator('#e-' + deep).waitFor();
    assert.ok(await page.locator('a', {hasText: 'The whole conversation'}).count() > 0, 'a branch page links the whole conversation');
    await centred(deep); await marked(deep);
    if (shots) await page.screenshot({path: shots + '/notifier-mobile-deep-390.png'});
    // Both seen: the inbox goes away, Me stays Me.
    await page.locator('#inbox-menu').waitFor({state: 'hidden'});
    assert.equal(await meLink.getAttribute('href'), '/me');
    assert.ok(await noHorizontalScroll(page), 'no horizontal scroll on the thread at 390px');

    // Desktop: the same two controls, side by side; mixed news is counted as new.
    const wide = await browser.newContext({viewport: {width: 1280, height: 800}}); await withIdentity(wide, me);
    await wide.addInitScript(([fp, id]) => { try { localStorage.setItem('swarmmemo.notify.v1:' + fp, JSON.stringify({fp, cursor: 'u0', polled_at: Date.now(), rooms: 1, requests: 0, replies: [id], addressed: []})); } catch (_) { /* opaque origins */ } }, [me.fingerprint, late]);
    const desk = await wide.newPage(); await b.attach(desk); desk.on('pageerror', e => errors.push(e.message));
    await desk.goto(origin + '/rooms');
    await desk.locator('#me-count-button').waitFor({state: 'visible'});
    assert.equal(await desk.locator('#me-count').textContent(), '2 new');
    assert.equal(await desk.locator('.workspace-link').getAttribute('href'), '/me');
    await desk.locator('#me-count-button').click();
    assert.deepEqual(await desk.locator('#inbox-items a').evaluateAll(list => list.map(a => a.getAttribute('href'))), ['/me#messages', '/e/' + late]);
    await desk.keyboard.press('Escape'); await desk.locator('#inbox-menu .inbox-panel').waitFor({state: 'hidden'});
    if (shots) await desk.locator('header.topbar').screenshot({path: shots + '/notifier-header-1280-c181.png'});
    await wide.close();
    assert.deepEqual(errors, []);
    console.log('PASS: phone header — Me opens Me with Settings one tap away; the inbox beside it says "2 replies", lists each by permalink, and each lands on the reply, centred and marked New, past the first page and deeper than the page indents.');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exit(1); });
