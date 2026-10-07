test.skipIf(true)("skipped", () => { expect(1).toBe(2); });
test.skipIf(false)("run1", () => { expect(1).toBe(1); });
test.runIf(true)("run2", () => { expect(2).toBe(2); });
test.runIf(false)("skipped2", () => { expect(1).toBe(2); });
test.sequential("seq", () => { expect(3).toBe(3); });
describe.sequential("g", () => { test("t", () => { expect(4).toBe(4); }); });
test.skipIf(false)("prints", () => { console.log(42); });
function main(): number { return 0; }
