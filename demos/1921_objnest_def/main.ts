interface In { v: i32; w: i32; }
interface Bx { p: In; }
function main(): i32 {
  const b: Bx = {p: {v: 6, w: 7}};
  const {p: {v = 9, w = 10}} = b;
  console.log(v + w);
  return 0;
}
