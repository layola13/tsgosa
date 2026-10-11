interface L3 { z: i32; }
interface In { v: i32; l: L3; }
interface Bx { p: In; }
function main(): i32 {
  const b: Bx = {p: {v: 6, l: {z: 3}}};
  const {p: {l: {z}}} = b;
  console.log(z);
  return 0;
}
