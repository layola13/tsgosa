class C {
  _v: i32;
  constructor(v: i32) { this._v = v; }
  set v(x: i32) { this._v = x * 2; }
  get v(): i32 { return this._v; }
}
function main(): i32 {
  const c = new C(5);
  c.v = 10;
  console.log(c.v);
  return 0;
}
