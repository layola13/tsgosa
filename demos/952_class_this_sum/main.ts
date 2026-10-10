class P {
  x: i32;
  constructor(x: i32) { this.x = x; }
  sum(y: i32): i32 { return this.x + y; }
}
function main(): i32 {
  const p = new P(30);
  console.log(p.sum(12));
  return 0;
}
