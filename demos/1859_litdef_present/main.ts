interface Pt { x: i32; }
function main(): i32 {
  const {x = 9} = {x: 5} as Pt;
  console.log(x);
  return 0;
}
