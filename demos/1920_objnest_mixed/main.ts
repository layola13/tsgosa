interface In { v: i32; }
interface Bx { p: In; q: i32; }
function main(): i32 {
  const b: Bx = {p: {v: 6}, q: 1};
  const {p: {v}, q} = b;
  console.log(v + q);
  return 0;
}
