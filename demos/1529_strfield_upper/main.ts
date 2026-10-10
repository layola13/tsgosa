interface U { name: string; age: i32; }
function main(): i32 {
  const u: U = { name: "hello", age: 5 };
  console.log(u.name.toUpperCase());
  console.log(u.age * 2);
  return 0;
}
