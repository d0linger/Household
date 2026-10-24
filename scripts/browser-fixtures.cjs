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
    for (const [date, odometer, quantity] of [['2025-01-02', '10100', '20'], ['2025-01-12', '10600', '30']]) {
      await create('fuel', 'fillups', {vehicle_id: vehicle.id, odometer_series_id: series.id, occurred_on: date, odometer, quantity, total_minor: '50,00', fuel_type: 'diesel', full_tank: true, station: 'Testtankstelle'});
    }
    const meter = await create('meters', 'meters', {name: 'Browser Strom ' + suffix, unit_code: 'kWh'});
    const device = await create('meters', 'devices', {meter_id: meter.id, serial_number: 'BROWSER-' + suffix, installed_on: '2025-01-01', initial_reading: '0'});
    for (const [date, value] of [['2025-01-02T12:00:00Z', '100'], ['2025-02-02T12:00:00Z', '150']]) {
      await create('meters', 'readings', {device_id: device.id, measured_at: date, value, kind: 'actual', note: 'Browser-Ablesung'});
    }
    return bootstrap;
  });
};
