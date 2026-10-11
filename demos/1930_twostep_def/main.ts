interface In { v: i32; }
interface Bx { p: In; }
function get(b: Bx): i32 {
  const {p} = b;
  const {v = 9} = p;
  return v;
}
function main(): i32 {
  const b: Bx = {p: {v: 6}};
  console.log(get(b));
  return 0;
}
