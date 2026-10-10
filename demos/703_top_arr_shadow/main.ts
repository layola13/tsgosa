const V = [1, 2];
function f(): i32 {
  const V = [9];
  return V[0];
}
function main(): i32 {
  console.log(f());
  console.log(V[1]);
  return 0;
}
