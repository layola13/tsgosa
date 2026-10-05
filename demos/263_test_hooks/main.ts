export function add(a: number, b: number): number {
  return a + b;
}
export function assertEq(actual: number, expected: number): void {
  if (actual != expected) {
    throw new Error("no");
  }
}
function runSuite(): number {
  let hits = 0;
  let passed = 0;
  beforeAll(() => {
    hits = hits + 5;
  });
  beforeEach(() => {
    hits = hits + 100;
  });
  afterEach(() => {
    hits = hits + 1000;
  });
  describe("outer", () => {
    beforeEach(() => {
      hits = hits + 10;
    });
    test("inner", () => {
      assertEq(add(1, 2), 3);
      hits = hits + 1;
      passed = passed + 1;
    });
    afterAll(() => {
      hits = hits + 7;
    });
  });
  test("after", () => {
    assertEq(add(2, 3), 5);
    hits = hits + 1;
    passed = passed + 1;
  });
  return hits * 10 + passed;
}
console.log(runSuite());
