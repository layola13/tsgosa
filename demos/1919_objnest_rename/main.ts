interface In { v: i32; w: i32; }
interface Bx { p: In; }
function main(): i32 {
  const b: Bx = {p: {v: 6, w: 7}};
  const {p: {v: x}} = b;
  console.log(x);
  return 0;
}
