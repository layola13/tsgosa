class P {
  x: i32;
  constructor(x: i32) { this.x = x; }
}
function main(): i32 {
  const a = new P(1);
  const b = new P(2);
  console.log(a.x + b.x);
  return 0;
}
