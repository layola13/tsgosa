interface Pt { x: i32; }
function main(): i32 {
  const o: Pt = {x: 5};
  const {z = 9} = o;
  console.log(z);
  return 0;
}
