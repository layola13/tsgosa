interface P {
  x: i32;
}
function main(): i32 {
  const p: P | null = null;
  console.log(p?.x ?? 0);
  const q: P | null = { x: 5 };
  console.log(q?.x ?? 0);
  return 0;
}
