interface Pt { x: i32; }
function main(): i32 {
  const {x} = {x: 5} satisfies Pt;
  console.log(x);
  return 0;
}
