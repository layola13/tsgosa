interface Pt {
  x: i32;
  y: i32;
}
function main(): i32 {
  const p: Pt = { x: 3, y: 4 };
  console.log(p.x + p.y);
  p.x = 10;
  console.log(p.x * p.y);
  return 0;
}
