'use strict';

// Fixtures use the actual API and belong only to the disposable review server.
module.exports = async function prepare(page) {
  return page.evaluate(async () => {
    let bootstrap = await fetch('/api/v1/core/bootstrap').then(r => r.json());
    const csrf = document.querySelector('meta[name="csrf-token"]').content;
    for (const module of bootstrap.modules) {
      const state = bootstrap.states[module.id];
      if (state.state === 'enabled') continue;
      const response = await fetch('/settings/modules/' + module.id, {method: 'POST', body: new URLSearchParams({csrf, enabled: 'true', version: String(state.version)})});
      if (!response.ok) throw Error('Could not enable synthetic module ' + module.id);
    }
    bootstrap = await fetch('/api/v1/core/bootstrap').then(r => r.json());
    const create = async (module, entity, data) => {
      if (module === 'fuel' && entity === 'fillups') {
        // Reviewed setup for the existing fixture, not a simulated human flow.
        for (const name of ['vehicle_id', 'odometer_series_id', 'occurred_on', 'fuel_type', 'odometer', 'quantity']) {
          if (typeof data[name] !== 'string' || !data[name].trim()) throw Error('Reviewed Fuel fixture needs explicit ' + name);
        }
        for (const name of ['full_tank', 'previous_fill_missing']) {
          if (typeof data[name] !== 'boolean') throw Error('Reviewed Fuel fixture needs explicit boolean ' + name);
        }
        const scopeResponse = await fetch('/api/v1/fuel/module?' + new URLSearchParams({pane: 'capture', vehicle: data.vehicle_id}));
        if (!scopeResponse.ok) throw Error('Current typed Fuel fixture scope unavailable');
        const scope = await scopeResponse.json(), unit = ({diesel: 'l', cng: 'kg'})[data.fuel_type];
        if (!unit || !scope.context.can_write || scope.context.generation !== bootstrap.states.fuel.generation || scope.context.currency_code !== 'EUR') throw Error('Known Diesel/CNG EUR fixture authority changed');
        if (typeof data.total_minor !== 'string' || !/^\d{1,12}(?:[.,]\d{1,2})?$/.test(data.total_minor)) throw Error('Fixture amount must be exact comma/dot EUR text');
        const [whole, fraction = ''] = data.total_minor.replace(',', '.').split('.'), {vehicle_id, ...values} = data;
        values.total_minor = (BigInt(whole) * 100n + BigInt(fraction.padEnd(2, '0'))).toString();
        values.quantity_unit = unit; values.currency_code = 'EUR'; values.acknowledged_warnings = [];
        const key = crypto.randomUUID();
        const command = {action: 'fillup.save', generation: scope.context.generation, vehicle_id, record_id: '', version: 0, source_stamp: scope.context.stamp, target_generations: {}, data: values};
        const reviewedPost = async (endpoint, body) => {
          const response = await fetch('/api/v1/fuel/module/' + endpoint, {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'Idempotency-Key': key}, body: JSON.stringify(body)});
          if (!response.ok) throw Error('Reviewed Fuel fixture ' + endpoint + ': ' + await response.text());
          return response.json();
        };
        let reviewed = await reviewedPost('preview', command);
        const codes = [...new Set((reviewed.warnings || []).map(warning => warning.code))];
        if (codes.length) {
          if (codes.some(code => typeof code !== 'string' || !code)) throw Error('Fixture warning has no explicit acknowledgement code');
          reviewed.command.data.acknowledged_warnings = codes;
          reviewed = await reviewedPost('preview', reviewed.command);
        }
        if (!reviewed.review || (reviewed.warnings || []).some(warning => !reviewed.command.data.acknowledged_warnings.includes(warning.code))) throw Error('Current fixture price warnings were not explicitly reviewed');
        const result = await reviewedPost('execute', {review: reviewed.review});
        if (!result.operation_id || !result.record_id) throw Error('Reviewed Fuel fixture returned no accepted receipt');
        return result.data || {id: result.record_id};
      }
      const response = await fetch(`/api/v1/m/${module}/${entity}`, {method: 'POST', headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf, 'Idempotency-Key': crypto.randomUUID()}, body: JSON.stringify({data, generation: bootstrap.states[module].generation})});
      if (!response.ok) throw Error(`Synthetic ${module}/${entity} fixture failed: ${response.status}`);
      return response.json();
    };
    const suffix = Date.now();
    const recipe = await create('recipes', 'recipes', {title: 'Browser Apfelrezept ' + suffix, base_servings: '2', summary: 'Synthetische Browser-Prüfdaten'});
    await create('recipes', 'ingredients', {recipe_id: recipe.id, name: 'Äpfel', quantity: '500', unit_code: 'g', position: '1'});
    await create('recipes', 'steps', {recipe_id: recipe.id, instruction: 'Äpfel waschen und schneiden.', position: '1', timer_seconds: '30'});
    const vehicle = await create('fuel', 'vehicles', {name: 'Browser Testfahrzeug ' + suffix, fuel_type: 'diesel'});
    const series = await create('fuel', 'odometer_series', {vehicle_id: vehicle.id, started_on: '2025-01-01', initial_reading: '10000', reason: 'Originalzähler'});
    // Pre-anchor history is unknown; the complete subsequent synthetic history
    // closes the documented 30 l / 500 km interval without inventing prior fills.
    for (const fill of [
      {occurred_on: '2025-01-02', odometer: '10100', quantity: '20', full_tank: true, previous_fill_missing: true},
      {occurred_on: '2025-01-12', odometer: '10600', quantity: '30', full_tank: true, previous_fill_missing: false}
    ]) {
      await create('fuel', 'fillups', {...fill, vehicle_id: vehicle.id, odometer_series_id: series.id, total_minor: '50,00', fuel_type: 'diesel', station: 'Testtankstelle'});
    }
    const meter = await create('meters', 'meters', {name: 'Browser Strom ' + suffix, unit_code: 'kWh'});
    const device = await create('meters', 'devices', {meter_id: meter.id, serial_number: 'BROWSER-' + suffix, installed_on: '2025-01-01', initial_reading: '0'});
    for (const [date, value] of [['2025-01-02T12:00:00Z', '100'], ['2025-02-02T12:00:00Z', '150']]) {
      await create('meters', 'readings', {device_id: device.id, measured_at: date, value, kind: 'actual', note: 'Browser-Ablesung'});
    }
    return bootstrap;
  });
};
