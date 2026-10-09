interface P {
  x: i32;
  s: string;
}
function main(): i32 {
  const p: P | null = null;
  console.log(p.s);
  return 0;
}
