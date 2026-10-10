class Box {
  v: i32 = 0;
}
function main(): i32 {
  const b: Box | null = null;
  const d = new Box();
  d.v = 9;
  const r: Box = b ?? d;
  console.log(r.v);
  return 0;
}
