interface Pt { x: i32; y: i32; }
function manhattan(p: Pt): i32 {
  return p.x + p.y;
}
function main(): i32 {
  const q: Pt = { x: 3, y: 4 };
  console.log(manhattan(q));
  return 0;
}