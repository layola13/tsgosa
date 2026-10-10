class C {
  #v: i32;
  constructor(v: i32) { this.#v = v; }
  eq(o: C): i32 { return this.#v === o.#v ? 1 : 0; }
}
function main(): i32 {
  const a = new C(5);
  const b = new C(5);
  const c = new C(6);
  console.log(a.eq(b));
  console.log(a.eq(c));
  return 0;
}
