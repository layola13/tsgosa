interface P {
  x: i32;
}
function main(): i32 {
  const p: P | null = null;
  console.log(p.x);
  return 0;
}
