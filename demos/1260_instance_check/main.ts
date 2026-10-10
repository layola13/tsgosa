class P {
  x: i32;
  constructor(x: i32) { this.x = x; }
}
function main(): i32 {
  const a = new P(1);
  console.log(a instanceof P ? 1 : 0);
  return 0;
}
