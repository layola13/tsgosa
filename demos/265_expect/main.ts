export function add(a: number, b: number): number {
  return a + b;
}
interface Pt { a: i32; b: i32; }
interface Nt { s: str; a: i32; }
interface At { xs: arr; n: i32; }
test("adds", () => {
  expect(add(1, 2)).toBe(3);
});
test("assert-count", () => {
  expect.assertions(2);
  expect(1).toBe(1);
  expect(1).not.toBe(2);
});
test("has-assert", () => {
  expect.hasAssertions();
  expect(1).toBeTruthy();
});
describe("more", () => {
  test("equal alias", () => {
    expect(add(2, 3)).toEqual(5);
  });
  test("strict", () => {
    expect(4).toStrictEqual(4);
  });
  test("negation", () => {
    expect(add(1, 2)).not.toBe(4);
    expect(add(2, 3)).not.toEqual(6);
  });
  test("zero-arity", () => {
    expect(0).toBeNull();
    expect(0).toBeUndefined();
    expect(1).toBeTruthy();
    expect(0).toBeFalsy();
    expect(1).not.toBeNull();
    expect(0).not.toBeTruthy();
    expect(1).toBeDefined();
    expect(0).not.toBeDefined();
    expect(1).not.toBeNaN();
  });
  test("compare", () => {
    expect(add(1, 2)).toBeGreaterThan(2);
    expect(add(1, 2)).toBeGreaterThanOrEqual(3);
    expect(add(1, 2)).toBeLessThan(4);
    expect(add(1, 2)).toBeLessThanOrEqual(3);
    expect(add(1, 2)).not.toBeGreaterThan(3);
    expect(add(1, 2)).not.toBeLessThan(3);
  });
  test("strings", () => {
    expect("hello").toContain("ell");
    expect("hello").toStartsWith("he");
    expect("hello").toEndsWith("lo");
    expect("hello").not.toContain("z");
    expect("hello").not.toStartsWith("lo");
    expect("hello").not.toEndsWith("he");
    expect("abc123").toMatch("c12");
    expect("abc123").not.toMatch("z9");
    expect("abc123").toMatch(/c[0-9]+/);
    expect("abc123").not.toMatch(/z[0-9]+/);
  });
  test("length", () => {
    expect([1, 2, 3]).toHaveLength(3);
    expect("hello").toHaveLength(5);
    expect([1, 2, 3]).not.toHaveLength(2);
  });
  test("throws", () => {
    expect(() => {
      throw new Error("x");
    }).toThrow();
    expect(() => {
      console.log("side");
    }).not.toThrow();
  });
  test("streq", () => {
    expect("a").toBe("a");
    expect("a").toEqual("a");
    expect("a").not.toBe("b");
    expect("hi " + "bo").toBe("hi bo");
  });
  test("inst-eq", () => {
    const p: Pt = { a: 1, b: 2 };
    const q: Pt = { a: 1, b: 2 };
    const r: Pt = { a: 1, b: 9 };
    expect(p).toEqual(q);
    expect(p).not.toEqual(r);
    expect(p).toBe(p);
  });
  test("inst-streq", () => {
    const m: Nt = { s: "hi", a: 1 };
    const n: Nt = { s: "hi", a: 1 };
    expect(m).toEqual(n);
  });
  test("inst-arreq", () => {
    const u: At = { xs: [1, 2], n: 3 };
    const v: At = { xs: [1, 2], n: 3 };
    expect(u).toEqual(v);
  });
});
console.log(42);
