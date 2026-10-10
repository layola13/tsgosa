const S = ["g", "h"];
function f(): string {
  const S = ["z"];
  return S[0];
}
function main(): i32 {
  console.log(f());
  console.log(S[1]);
  return 0;
}
