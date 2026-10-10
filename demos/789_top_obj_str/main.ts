interface Q { name: string; n: i32 }
const O: Q = {name: "ab", n: 5};
function main(): i32 {
  console.log(O.name);
  console.log(O.n);
  return 0;
}
