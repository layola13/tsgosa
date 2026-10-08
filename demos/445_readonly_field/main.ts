interface P {
  readonly x: i32;
}
function main(): i32 {
  const o: P = { x: 9 };
  console.log(o.x);
  return 0;
}
