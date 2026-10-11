interface Pt { s: string; x: i32; }
function main(): i32 {
  const o: Pt = {s: "hi", x: 3};
  const {s = "d", x = 9} = o;
  console.log(s);
  console.log(x);
  return 0;
}
