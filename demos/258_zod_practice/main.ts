import { z } from "zod";

// ==========================================
// 1. 基础类型、标量与内置校验规则
// ==========================================

const SystemRoleSchema = z.enum(["admin", "editor", "viewer", "guest"]);

// 原生枚举 (Native Enum)
enum AuthProvider {
  EMAIL = "EMAIL",
  GITHUB = "GITHUB",
  GOOGLE = "GOOGLE",
}
const AuthProviderSchema = z.nativeEnum(AuthProvider);

// 各种标量类型与链式校验
const UserProfileSchema = z.object({
  // 字符串及预置校验 (UUID, Email, Regex, CUID 等)
  id: z.string().uuid("Invalid UUID format"),
  username: z
    .string()
    .min(3, "Username must be >= 3 characters")
    .max(20, "Username must be <= 20 characters")
    .regex(/^[a-zA-Z0-9_]+$/, "Alphanumeric and underscores only")
    .trim()
    .toLowerCase(),
  email: z.string().email("Invalid email format"),
  avatarUrl: z.string().url().optional(), // 可选字段: string | undefined

  // 数值与区间校验
  age: z
    .number()
    .int("Must be an integer")
    .positive("Must be greater than 0")
    .lte(120, "Age cannot exceed 120"),
  accountBalance: z.bigint().nonnegative(),

  // 布尔值与字面量 (Literal)
  isActive: z.boolean().default(true),
  termsAcceptedVersion: z.literal("v2.1"), // 严格单值

  // 日期类型
  createdAt: z.date().default(() => new Date()),

  // 特殊基础类型: null, undefined, unknown, any, never, void
  notes: z.string().nullable(), // string | null
  middleName: z.string().nullish(), // string | null | undefined
  internalToken: z.undefined(),
  metadata: z.record(z.string(), z.unknown()), // Record<string, unknown>
  rawPayload: z.any().optional(),
});

// ==========================================
// 2. 复合结构：Array, Tuple, Map, Set, Promise
// ==========================================

const ComplexCollectionsSchema = z.object({
  // Array: 长度约束、元素去重
  tags: z.array(z.string().min(1)).min(1).max(10),

  // Tuple: 固定长度和类型的数组，支持可选和 rest
  coordinates: z.tuple([
    z.number(), // 纬度
    z.number(), // 经度
    z.number().optional(), // 海拔 (可选)
  ]),

  // Set 与 Map
  permissions: z.set(z.string()).min(1),
  featureFlags: z.map(z.string(), z.boolean()),

  // Promise 类型 (异步验证时常用)
  asyncComputation: z.promise(z.number()),
});

// ==========================================
// 3. 联合类型、交叉类型与可辨识联合 (Discriminated Union)
// ==========================================

// 可辨识联合 (推荐：性能优异且类型推导精确)
const CreditCardPayment = z.object({
  type: z.literal("credit_card"),
  cardNumber: z.string().regex(/^\d{16}$/, "Must be 16 digits"),
  cvv: z.string().length(3),
});

const CryptoPayment = z.object({
  type: z.literal("crypto"),
  walletAddress: z.string().startsWith("0x"),
  network: z.enum(["ethereum", "polygon"]),
});

const PaymentMethodSchema = z.discriminatedUnion("type", [
  CreditCardPayment,
  CryptoPayment,
]);

// 普通联合类型 (Union)
const ContactInfoSchema = z.union([
  z.object({ phone: z.string().min(10) }),
  z.object({ email: z.string().email() }),
]);

// 交叉类型 (Intersection)
const TimestampsSchema = z.object({
  createdAt: z.date(),
  updatedAt: z.date(),
});
const SoftDeletableSchema = z.object({
  deletedAt: z.date().nullable(),
});
const AuditAuditInfoSchema = z.intersection(TimestampsSchema, SoftDeletableSchema);
// 等价简写: TimestampsSchema.and(SoftDeletableSchema)

// ==========================================
// 4. 递归模式 (Recursive / Self-Referencing)
// ==========================================

interface Category {
  name: string;
  subcategories: Category[];
}

const CategorySchema: z.ZodType<Category> = z.lazy(() =>
  z.object({
    name: z.string().min(1),
    subcategories: z.array(CategorySchema),
  })
);

// ==========================================
// 5. 数据转换 (Transform, Coerce, Preprocess, Pipeline)
// ==========================================

const DataProcessingSchema = z.object({
  // Coerce: 强制隐式类型转换 (常用于 URL query 参数或表单数据)
  page: z.coerce.number().int().positive().default(1),
  isExport: z.coerce.boolean().default(false),

  // Preprocess: 在校验前拦截并修改原始数据
  csvTags: z.preprocess((val) => {
    if (typeof val === "string") return val.split(",").map((s) => s.trim());
    return val;
  }, z.array(z.string())),

  // Transform: 校验成功后修改输出格式
  taxRate: z
    .number()
    .min(0)
    .max(1)
    .transform((val) => `${(val * 100).toFixed(2)}%`),

  // Pipeline (管道模式): 前一个 Schema 的输出作为后一个 Schema 的输入
  numericStringToInt: z
    .string()
    .regex(/^\d+$/)
    .transform((val) => parseInt(val, 10))
    .pipe(z.number().max(10000)),
});

// ==========================================
// 6. 自定义校验 (Refine, SuperRefine) 与多字段交叉验证
// ==========================================

const PasswordResetSchema = z
  .object({
    password: z.string().min(8, "Password must be at least 8 characters"),
    confirmPassword: z.string(),
    backupEmail: z.string().email().optional(),
    currentEmail: z.string().email(),
  })
  // 简单单条业务规则 refine
  .refine((data) => data.password === data.confirmPassword, {
    message: "Passwords must match",
    path: ["confirmPassword"], // 将错误精确挂载到特定字段
  })
  // 复杂的低阶多规则 superRefine (支持生成多条细粒度错误)
  .superRefine((data, ctx) => {
    // 密码不能包含邮箱前缀
    const emailPrefix = data.currentEmail.split("@")[0];
    if (data.password.toLowerCase().includes(emailPrefix.toLowerCase())) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "Password cannot contain parts of your email",
        path: ["password"],
      });
    }

    // 备用邮箱不能与当前邮箱相同
    if (data.backupEmail && data.backupEmail === data.currentEmail) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: "Backup email must be different from current email",
        path: ["backupEmail"],
      });
    }
  });

// 异步自定义校验 (如检查数据库用户名是否重复)
const AsyncUsernameSchema = z.string().refine(async (username) => {
  // 模拟异步 API/DB 调用
  await new Promise((resolve) => setTimeout(resolve, 50));
  return username !== "superuser";
}, { message: "Username 'superuser' is reserved" });

// ==========================================
// 7. 对象修饰符 (Extend, Merge, Pick, Omit, Partial, Passthrough)
// ==========================================

const BaseEntitySchema = z.object({
  id: z.string().uuid(),
  createdAt: z.date(),
});

// extend 与 merge
const UserSchema = BaseEntitySchema.extend({
  role: SystemRoleSchema,
  authProvider: AuthProviderSchema,
  profile: UserProfileSchema,
  payment: PaymentMethodSchema,
  nestedCategories: z.array(CategorySchema),
  processing: DataProcessingSchema,
});

// 结构裁剪与修饰
const CreateUserDTO = UserSchema.omit({ id: true, createdAt: true }); // 排除字段
const UserSummaryDTO = UserSchema.pick({ id: true, role: true });      // 选取字段
const UpdateUserDTO = CreateUserDTO.partial();                       // 所有字段转为可选
const RequiredUserDTO = UpdateUserDTO.required();                    // 所有字段转为必填

// 未知属性处理策略
const StrictSchema = BaseEntitySchema.strict();       // 传入多余字段时报错
const PassthroughSchema = BaseEntitySchema.passthrough(); // 保留多余字段
const StripSchema = BaseEntitySchema.strip();         // 默认行为：移除多余字段

// ==========================================
// 8. 运行时方法封装 (z.function)
// ==========================================

// 对函数的参数与返回值进行运行时校验
const CalculateDiscountFunction = z
  .function()
  .args(z.number().positive(), z.number().min(0).max(1)) // 参数：原价, 折扣率
  .returns(z.number())                                    // 返回值
  .implement((price, discount) => {
    return price * (1 - discount);
  });

// ==========================================
// 9. TypeScript 类型推导 (Inference)
// ==========================================

// 推导校验通过后的输出类型 (Output)
export type User = z.infer<typeof UserSchema>;
export type PaymentMethod = z.infer<typeof PaymentMethodSchema>;

// 当存在 transform / coerce / default 时，Input 和 Output 类型可能不同
export type DataProcessingInput = z.input<typeof DataProcessingSchema>;
export type DataProcessingOutput = z.output<typeof DataProcessingSchema>;

// ==========================================
// 10. 解析与错误处理实践
// ==========================================

function handlePayload(input: unknown) {
  // 1. 同步安全解析 (safeParse) - 推荐在服务端请求入口使用
  const result = PasswordResetSchema.safeParse(input);

  if (!result.success) {
    // 格式化错误信息
    const formattedErrors = result.error.format();
    const flattenedErrors = result.error.flatten();

    console.error("Flattened field errors:", flattenedErrors.fieldErrors);
    return { status: 400, errors: formattedErrors };
  }

  // 成功时直接拿到强类型数据
  return { status: 200, data: result.data };
}

async function handleAsyncPayload(input: unknown) {
  // 2. 异步解析 (parseAsync / safeParseAsync)
  try {
    const validUsername = await AsyncUsernameSchema.parseAsync(input);
    return validUsername;
  } catch (err) {
    if (err instanceof z.ZodError) {
      console.error("Zod validation failed issues:", err.issues);
    }
    throw err;
  }
}
