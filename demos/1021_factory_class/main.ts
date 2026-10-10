class O {
  v: i32;
  constructor(v: i32) { this.v = v; }
}
function make(v: i32): O {
  return new O(v + 1);
}
function main(): i32 {
  console.log(make(41).v);
  return 0;
}
