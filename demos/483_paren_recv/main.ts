class C {
  v: i32 = 0;
  get(): i32 { return this.v + 1; }
}
function main(): i32 {
  const c = new C();
  c.v = 41;
  console.log((c).get());
  console.log((c).v);
  (c).v = 7;
  console.log((c).v);
  return 0;
}
