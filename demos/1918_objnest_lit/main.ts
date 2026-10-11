interface In { v: i32; }
interface Bx { p: In; }
function main(): i32 {
  const {p: {v}} = {p: {v: 6}} as Bx;
  console.log(v);
  return 0;
}
