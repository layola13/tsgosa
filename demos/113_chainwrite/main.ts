interface Pt { x: i32; y: i32; }
interface Wrap { p: Pt; }
function main(): i32 {
  const q: Wrap = { p: { x: 1, y: 2 } };
  q.p.x = 10;
  console.log(q.p.x + q.p.y);
  return 0;
}
