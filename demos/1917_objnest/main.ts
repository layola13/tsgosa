interface In { v: i32; }
interface Bx { p: In; }
function main(): i32 {
  const b: Bx = {p: {v: 6}};
  const {p: {v}} = b;
  console.log(v);
  return 0;
}
