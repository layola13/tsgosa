interface Pt { x: i32; }
function main(): i32 {
  const o: Pt = {x: 5};
  const {x = 1 + 2} = o;
  console.log(x);
  return 0;
}
